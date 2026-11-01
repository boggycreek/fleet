// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

// PingPayload defines the lightweight push-notification JSON schema
type PingPayload struct {
	Type   string `json:"type"`
	Stream string `json:"stream"`
	Sender string `json:"sender"`
	TS     int64  `json:"ts"`
}

// PublishPing broadcasts a lightweight notification ping to channel ping:<recipient>.
func (c *Client) PublishPing(ctx context.Context, recipient string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r := strings.ToLower(strings.TrimSpace(recipient))
	if r == "" {
		return errors.New("recipient cannot be empty")
	}

	channel := PingKey(r)
	payload := PingPayload{
		Type:   "ping",
		Stream: InboxKey(r),
		Sender: strings.ToLower(strings.TrimSpace(c.cfg.AgentID)),
		TS:     time.Now().UnixMilli(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed marshaling ping payload: %w", err)
	}

	_, err = c.Exec(ctx, "PUBLISH", channel, string(data))
	if err != nil {
		return fmt.Errorf("failed publishing ping to %s: %w", channel, err)
	}
	return nil
}

// SubscribePing establishes a dedicated Pub/Sub connection to Valkey, subscribes to the
// specified channels, and returns a read-only channel delivering incoming ping payloads.
// It cleanly closes the connection and the output channel upon context cancellation or socket error.
func (c *Client) SubscribePing(ctx context.Context, channels ...string) (<-chan string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, errors.New("at least one channel required for subscription")
	}

	var subChannels []string
	for _, ch := range channels {
		trimmed := strings.ToLower(strings.TrimSpace(ch))
		if trimmed == "" {
			return nil, errors.New("channel name cannot be empty")
		}
		subChannels = append(subChannels, trimmed)
	}

	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	dialer := net.Dialer{Timeout: c.cfg.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to Valkey at %s: %w", addr, err)
	}

	reader := resp.NewReader(conn)
	writer := resp.NewWriter(conn)

	// Authenticate if password configured
	if c.cfg.Password != "" {
		user := c.cfg.Username
		if user == "" && c.cfg.AgentID != "" {
			user = c.cfg.AgentID
		}
		var authCmd []string
		if user != "" {
			authCmd = []string{"AUTH", user, c.cfg.Password}
		} else {
			authCmd = []string{"AUTH", c.cfg.Password}
		}
		if err := writer.WriteCommand(authCmd...); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed sending auth command: %w", err)
		}
		authVal, err := reader.ReadValue()
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed reading auth response: %w", err)
		}
		if authVal.Type == resp.TypeError {
			_ = conn.Close()
			return nil, fmt.Errorf("authentication error: %s", authVal.Str)
		}
	}

	// Issue SUBSCRIBE command
	subCmd := append([]string{"SUBSCRIBE"}, subChannels...)
	if err := writer.WriteCommand(subCmd...); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed sending subscribe command: %w", err)
	}

	// Read initial subscription confirmations for each channel
	for range subChannels {
		val, err := reader.ReadValue()
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed reading subscribe confirmation: %w", err)
		}
		if val.Type == resp.TypeError {
			_ = conn.Close()
			return nil, fmt.Errorf("subscription error: %s", val.Str)
		}
	}

	out := make(chan string, 100)

	go func() {
		defer func() {
			_ = conn.Close()
			close(out)
		}()

		var done atomic.Bool
		var stop func() bool
		if ctx.Done() != nil {
			stop = context.AfterFunc(ctx, func() {
				if !done.Load() {
					_ = conn.SetDeadline(time.Unix(1, 0))
				}
			})
		}
		defer func() {
			done.Store(true)
			if stop != nil {
				stop()
			}
		}()

		for {
			if ctx.Err() != nil {
				return
			}

			val, err := reader.ReadValue()
			if err != nil {
				return
			}

			// Pub/Sub frames are RESP arrays: ["message", channel, payload]
			arr, err := val.AsArray()
			if err != nil || len(arr) < 3 {
				continue
			}

			kind, err := arr[0].AsString()
			if err != nil || !strings.EqualFold(kind, "message") {
				continue
			}

			payload, err := arr[2].AsString()
			if err != nil {
				continue
			}

			select {
			case <-ctx.Done():
				return
			case out <- payload:
			}
		}
	}()

	return out, nil
}
