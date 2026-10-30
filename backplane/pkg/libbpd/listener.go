// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbpd

import (
	"context"
	"errors"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

// StreamReceiver abstracts stream polling and reading operations from libbp.Client.
type StreamReceiver interface {
	Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error)
}

// MessageHandler is invoked when a new message is received from a stream.
type MessageHandler func(ctx context.Context, msg *libbp.Message) error

// StreamListener provides blocking stream listening on Valkey streams via XREAD BLOCK.
type StreamListener struct {
	receiver StreamReceiver
	blockSec int
	backoff  time.Duration
}

// NewStreamListener creates a new StreamListener with a specified block timeout in seconds.
// If blockSeconds <= 0, a default of 5 seconds is used.
func NewStreamListener(receiver StreamReceiver, blockSeconds int) *StreamListener {
	if blockSeconds <= 0 {
		blockSeconds = 5
	}
	return &StreamListener{
		receiver: receiver,
		blockSec: blockSeconds,
		backoff:  100 * time.Millisecond,
	}
}

// SetBackoff configures the backoff pause duration on transient read errors.
func (l *StreamListener) SetBackoff(d time.Duration) {
	if d > 0 {
		l.backoff = d
	}
}

// BlockSeconds returns the configured block timeout in seconds.
func (l *StreamListener) BlockSeconds() int {
	return l.blockSec
}

// Listen blocks on stream reception and invokes handler for each received message.
// It continues listening until ctx is canceled or receiver returns an unrecoverable error.
func (l *StreamListener) Listen(ctx context.Context, handler MessageHandler) error {
	if l.receiver == nil {
		return errors.New("stream receiver cannot be nil")
	}
	if handler == nil {
		return errors.New("message handler cannot be nil")
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		messages, err := l.receiver.Recv(ctx, l.blockSec)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Transient poll/read error: wait backoff before retrying
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(l.backoff):
				continue
			}
		}

		for _, msg := range messages {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := handler(ctx, msg); err != nil {
				// Handler execution error; continue processing subsequent messages
				continue
			}
		}
	}
}
