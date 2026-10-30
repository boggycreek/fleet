// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp/resp"
)

// ErrClosed is returned when an operation is attempted on a closed or broken connection.
var ErrClosed = errors.New("client connection closed")

// ClientConfig configuration parameters for connecting to the backplane
type ClientConfig struct {
	Host           string
	Port           int
	Username       string
	Password       string
	AgentID        string
	Mode           string
	SigningKey     ed25519.PrivateKey
	SigningKeyPEM  string
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
}

// Client represents an authenticated connection to the Valkey Backplane
type Client struct {
	cfg       ClientConfig
	conn      net.Conn
	reader    *resp.Reader
	writer    *resp.Writer
	mu        sync.Mutex
	closed    bool
	broken    bool
	lockCh    chan struct{}
	pubKeys   map[string]ed25519.PublicKey
	pubKeysMu sync.RWMutex
}

func (c *Client) connect(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	dialer := net.Dialer{Timeout: c.cfg.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("failed connecting to Valkey at %s: %w", addr, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.reader = resp.NewReader(conn)
	c.writer = resp.NewWriter(conn)
	c.broken = false
	c.mu.Unlock()

	// Authenticate if password provided
	if c.cfg.Password != "" {
		var authVal resp.Value
		user := c.cfg.Username
		if user == "" && c.cfg.AgentID != "" {
			user = c.cfg.AgentID
		}
		if user != "" {
			authVal, err = c.execInternal(ctx, "AUTH", user, c.cfg.Password)
		} else {
			authVal, err = c.execInternal(ctx, "AUTH", c.cfg.Password)
		}
		if err != nil {
			c.markBroken()
			return fmt.Errorf("authentication failed: %w", err)
		}
		if authVal.Type == resp.TypeError {
			c.markBroken()
			return fmt.Errorf("authentication error: %s", authVal.Str)
		}
	}

	return nil
}

// Dial connects and authenticates to the Valkey Backplane
func Dial(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	if cfg.Port == 0 {
		cfg.Port = 6379
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.AgentID == "" {
		cfg.AgentID = cfg.Username
	}

	// Parse signing key if PEM provided
	if cfg.SigningKey == nil && cfg.SigningKeyPEM != "" {
		priv, err := DecodePrivateKeyPEM(cfg.SigningKeyPEM)
		if err == nil {
			cfg.SigningKey = priv
		}
	}

	c := &Client{
		cfg:     cfg,
		lockCh:  make(chan struct{}, 1),
		pubKeys: make(map[string]ed25519.PublicKey),
	}
	c.lockCh <- struct{}{}

	if err := c.connect(ctx); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.lockCh:
		return nil
	}
}

func (c *Client) unlock() {
	c.lockCh <- struct{}{}
}

func (c *Client) markBroken() {
	c.mu.Lock()
	c.broken = true
	conn := c.conn
	c.conn = nil
	c.reader = nil
	c.writer = nil
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *Client) isBroken() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.broken || c.conn == nil
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Close closes the underlying network connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.broken = true
	conn := c.conn
	c.conn = nil
	c.reader = nil
	c.writer = nil
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// Exec sends a raw command and returns the RESP value
func (c *Client) Exec(ctx context.Context, cmd string, args ...string) (resp.Value, error) {
	if err := ctx.Err(); err != nil {
		return resp.Value{}, err
	}
	if err := AssertAllowed(c.cfg.AgentID, cmd, args...); err != nil {
		return resp.Value{}, err
	}
	if err := c.lock(ctx); err != nil {
		return resp.Value{}, err
	}
	defer c.unlock()
	return c.execInternal(ctx, cmd, args...)
}

func (c *Client) execInternal(ctx context.Context, cmd string, args ...string) (resp.Value, error) {
	if c.isClosed() {
		return resp.Value{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return resp.Value{}, err
	}

	if c.isBroken() {
		if err := c.connect(ctx); err != nil {
			return resp.Value{}, err
		}
	}

	minBlockDuration := time.Duration(0)
	if strings.EqualFold(cmd, "XREAD") || strings.EqualFold(cmd, "BRPOP") || strings.EqualFold(cmd, "BLPOP") {
		for i, arg := range args {
			if strings.EqualFold(arg, "BLOCK") && i+1 < len(args) {
				if ms, err := strconv.Atoi(args[i+1]); err == nil && ms > 0 {
					minBlockDuration = time.Duration(ms)*time.Millisecond + 5*time.Second
				}
				break
			}
		}
	}

	c.mu.Lock()
	activeConn := c.conn
	c.mu.Unlock()

	if activeConn != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = activeConn.SetDeadline(deadline)
		} else if minBlockDuration > 0 {
			if c.cfg.ReadTimeout > minBlockDuration {
				_ = activeConn.SetDeadline(time.Now().Add(c.cfg.ReadTimeout))
			} else {
				_ = activeConn.SetDeadline(time.Now().Add(minBlockDuration))
			}
		} else if c.cfg.ReadTimeout > 0 {
			_ = activeConn.SetDeadline(time.Now().Add(c.cfg.ReadTimeout))
		} else {
			_ = activeConn.SetDeadline(time.Time{})
		}
		defer func() {
			if !c.isClosed() && !c.isBroken() {
				_ = activeConn.SetDeadline(time.Time{})
			}
		}()

		if ctx.Done() != nil {
			stop := context.AfterFunc(ctx, func() {
				_ = activeConn.SetDeadline(time.Unix(1, 0))
			})
			defer stop()
		}
	}

	fullCmd := append([]string{cmd}, args...)
	if err := c.writer.WriteCommand(fullCmd...); err != nil {
		c.markBroken()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return resp.Value{}, ctxErr
		}
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return resp.Value{}, context.DeadlineExceeded
		}
		return resp.Value{}, err
	}
	val, err := c.reader.ReadValue()
	if err != nil {
		c.markBroken()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return resp.Value{}, ctxErr
		}
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return resp.Value{}, context.DeadlineExceeded
		}
		return resp.Value{}, err
	}
	if val.Type == resp.TypeError {
		return val, resp.ReadError(val)
	}
	return val, nil
}

// NextSeq increments and returns the next monotonic sequence number for the agent
func (c *Client) NextSeq(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	seqKey := SeqKey(c.cfg.AgentID)
	val, err := c.Exec(ctx, "INCR", seqKey)
	if err != nil {
		return 0, err
	}
	return val.AsInt()
}

// publishMessage is a shared helper for broadcasting or directly delivering messages to Valkey streams.
func (c *Client) publishMessage(ctx context.Context, stream, destination, content, blobPath string, replyTo string) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	seq, err := c.NextSeq(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed generating sequence number: %w", err)
	}

	now := time.Now().UnixMilli()
	msg := &Message{
		Sender:      c.cfg.AgentID,
		Destination: destination,
		Content:     content,
		BlobPath:    blobPath,
		Timestamp:   now,
		Seq:         seq,
		Citation:    FormatCitation(c.cfg.AgentID, seq),
		ReplyTo:     replyTo,
	}

	if c.cfg.SigningKey != nil {
		msg.Signature = SignPayload(c.cfg.SigningKey, msg.SigningPayload())
		msg.IsSigned = true
		msg.IsVerified = true
	}

	args := append([]string{stream, "*"}, msg.ToFieldValues()...)
	val, err := c.Exec(ctx, "XADD", args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("failed publishing message to %s: %w", stream, err)
	}

	msgID, err := val.AsString()
	if err != nil {
		return nil, err
	}
	msg.ID = msgID
	return msg, nil
}

// Say broadcasts a message to the fleet (<id>:out)
func (c *Client) Say(ctx context.Context, content string, blobPath string) (*Message, error) {
	return c.publishMessage(ctx, OutKey(c.cfg.AgentID), "", content, blobPath, "")
}

// Tell sends a direct point-to-point message to an agent's inbox (<recipient>:inbox)
func (c *Client) Tell(ctx context.Context, recipient string, content string) (*Message, error) {
	return c.TellWithBlob(ctx, recipient, content, "")
}

// TellWithBlob sends a direct point-to-point message with an optional parked blob attachment
func (c *Client) TellWithBlob(ctx context.Context, recipient string, content string, blobPath string) (*Message, error) {
	return c.publishMessage(ctx, InboxKey(recipient), recipient, content, blobPath, "")
}

// Post publishes a long-form message by parking the payload as a blob and setting the summary as content.
// If recipient is empty, it broadcasts to the fleet; otherwise it sends a direct point-to-point message.
func (c *Client) Post(ctx context.Context, recipient string, summary string, payload []byte) (*Message, error) {
	blobKey, err := c.ParkBlob(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("failed parking payload blob: %w", err)
	}

	if recipient == "" {
		return c.Say(ctx, summary, blobKey)
	}
	return c.TellWithBlob(ctx, recipient, summary, blobKey)
}

// Reply sends a threaded direct message citing a parent message ID
func (c *Client) Reply(ctx context.Context, replyToMsgID string, recipient string, content string) (*Message, error) {
	return c.publishMessage(ctx, InboxKey(recipient), recipient, content, "", replyToMsgID)
}

// JoinEnsemble registers the agent into the specified ensemble channel set (<agent>:ensembles)
func (c *Client) JoinEnsemble(ctx context.Context, ensembleName string) error {
	if err := ValidateEnsembleName(ensembleName); err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(ensembleName))
	_, err := c.Exec(ctx, "SADD", AgentEnsemblesKey(c.cfg.AgentID), name)
	return err
}

// LeaveEnsemble removes the agent from the specified ensemble channel set (<agent>:ensembles)
func (c *Client) LeaveEnsemble(ctx context.Context, ensembleName string) error {
	if err := ValidateEnsembleName(ensembleName); err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(ensembleName))
	_, err := c.Exec(ctx, "SREM", AgentEnsemblesKey(c.cfg.AgentID), name)
	return err
}

// ListJoinedEnsembles retrieves the list of ensembles this agent has joined
func (c *Client) ListJoinedEnsembles(ctx context.Context) ([]string, error) {
	val, err := c.Exec(ctx, "SMEMBERS", AgentEnsemblesKey(c.cfg.AgentID))
	if err != nil {
		return nil, err
	}
	items, err := val.AsArray()
	if err != nil {
		return nil, err
	}
	var res []string
	for _, item := range items {
		s, err := item.AsString()
		if err == nil && s != "" {
			res = append(res, s)
		}
	}
	sort.Strings(res)
	return res, nil
}

// PublishEnsemble broadcasts a message to a team-scoped ensemble stream (ensemble:<name>:out)
func (c *Client) PublishEnsemble(ctx context.Context, ensembleName, content string, blobPath ...string) (*Message, error) {
	if err := ValidateEnsembleName(ensembleName); err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(ensembleName))
	bp := ""
	if len(blobPath) > 0 {
		bp = blobPath[0]
	}
	return c.publishMessage(ctx, EnsembleOutKey(name), "ensemble:"+name, content, bp, "")
}

// ReadEnsembleMessages reads messages from an ensemble stream starting after lastID
func (c *Client) ReadEnsembleMessages(ctx context.Context, ensembleName, lastID string, count int) ([]*Message, error) {
	if err := ValidateEnsembleName(ensembleName); err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(ensembleName))
	stream := EnsembleOutKey(name)
	if lastID == "" {
		lastID = "0-0"
	}
	if count <= 0 {
		count = 50
	}
	val, err := c.Exec(ctx, "XREAD", "COUNT", strconv.Itoa(count), "STREAMS", stream, lastID)
	if err != nil {
		return nil, err
	}
	return c.parseXReadResponse(ctx, val)
}

// GetCursor retrieves the last read stream ID for a given stream key
func (c *Client) GetCursor(ctx context.Context, streamKey string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cursorKey := CursorKey(c.cfg.AgentID)
	val, err := c.Exec(ctx, "HGET", cursorKey, streamKey)
	if err != nil {
		return "", err
	}
	if val.IsNull {
		return "0-0", nil
	}
	return val.AsString()
}

// SetCursor saves the read cursor for a given stream key
func (c *Client) SetCursor(ctx context.Context, streamKey string, lastID string) error {
	cursorKey := CursorKey(c.cfg.AgentID)
	_, err := c.Exec(ctx, "HSET", cursorKey, streamKey, lastID)
	return err
}

// Recv reads new messages from own inbox and all peer broadcast streams since last cursor
func (c *Client) Recv(ctx context.Context, blockSeconds int) ([]*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	peers, err := c.Peers(ctx)
	if err != nil {
		return nil, err
	}

	// Discover all active streams (inbox + all *:out streams)
	myInbox := InboxKey(c.cfg.AgentID)
	streams := make([]string, 0, len(peers)+1)
	streams = append(streams, myInbox)

	for _, p := range peers {
		if strings.ToLower(p.ID) != strings.ToLower(c.cfg.AgentID) {
			streams = append(streams, OutKey(p.ID))
		}
	}

	var streamArgs []string
	var cursors []string

	for _, s := range streams {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cur, err := c.GetCursor(ctx, s)
		if err != nil {
			return nil, err
		}
		if cur == "" {
			cur = "0-0"
		}
		streamArgs = append(streamArgs, s)
		cursors = append(cursors, cur)
	}

	args := []string{}
	if blockSeconds > 0 {
		args = append(args, "BLOCK", strconv.Itoa(blockSeconds*1000))
	}
	args = append(args, "STREAMS")
	args = append(args, streamArgs...)
	args = append(args, cursors...)

	val, err := c.Exec(ctx, "XREAD", args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, resp.ErrNil) {
			return []*Message{}, nil
		}
		return nil, err
	}
	if val.IsNull {
		return []*Message{}, nil
	}

	return c.parseXReadResponse(ctx, val)
}

// Human returns messages from the authoritative human operator broadcast log newest-first
func (c *Client) Human(ctx context.Context, count int) ([]*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 20
	}
	// Discover human name
	humanName := "human"
	nameVal, err := c.Exec(ctx, "GET", KeyHumanName)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && !nameVal.IsNull {
		if nameStr, err := nameVal.AsString(); err == nil && nameStr != "" {
			humanName = nameStr
		}
	}

	humanOut := OutKey(humanName)
	val, err := c.Exec(ctx, "XREVRANGE", humanOut, "+", "-", "COUNT", strconv.Itoa(count))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	arr, err := val.AsArray()
	if err != nil {
		return []*Message{}, nil
	}

	var messages []*Message
	for _, item := range arr {
		entry, err := item.AsArray()
		if err != nil || len(entry) < 2 {
			continue
		}
		id := entry[0].String()
		fields, err := entry[1].AsMap()
		if err != nil {
			continue
		}
		fieldMap := make(map[string]string, len(fields))
		for k, v := range fields {
			fieldMap[k] = v.String()
		}
		msg := MessageFromFields(id, fieldMap)
		c.VerifyMessage(ctx, msg, true)
		messages = append(messages, msg)
	}
	return messages, nil
}

// Peers returns the discovered list of active agents and their statuses
func (c *Client) Peers(ctx context.Context) ([]*Peer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var agentIDs []string

	// Scan for identity:* keys across all pages
	cursor := "0"
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		val, err := c.Exec(ctx, "SCAN", cursor, "MATCH", "identity:*", "COUNT", "100")
		if err != nil {
			return nil, err
		}
		arr, err := val.AsArray()
		if err != nil || len(arr) < 2 {
			break
		}
		cursor = arr[0].String()
		if keysArray, err := arr[1].AsArray(); err == nil {
			for _, k := range keysArray {
				agentID := strings.TrimPrefix(k.String(), "identity:")
				if agentID != "" && !seen[agentID] {
					seen[agentID] = true
					agentIDs = append(agentIDs, agentID)
				}
			}
		}
		if cursor == "0" || cursor == "" {
			break
		}
	}

	// Scan for legacy *:out keys across all pages
	cursor = "0"
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		valOut, err := c.Exec(ctx, "SCAN", cursor, "MATCH", "*:out", "COUNT", "100")
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			break
		}
		arr, err := valOut.AsArray()
		if err != nil || len(arr) < 2 {
			break
		}
		cursor = arr[0].String()
		if keysArray, err := arr[1].AsArray(); err == nil {
			for _, k := range keysArray {
				keyStr := k.String()
				if strings.HasSuffix(keyStr, ":out") {
					agentID := strings.TrimSuffix(keyStr, ":out")
					if agentID != "" && !seen[agentID] {
						seen[agentID] = true
						agentIDs = append(agentIDs, agentID)
					}
				}
			}
		}
		if cursor == "0" || cursor == "" {
			break
		}
	}

	currentLiaison, err := c.GetLiaison(ctx)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	peers := make([]*Peer, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		role := ""
		if rec, err := c.GetIdentity(ctx, agentID); err == nil && rec != nil {
			if rec.Kind == "human" {
				continue
			}
			role = rec.Role
		} else if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		status, err := c.GetStatus(ctx, agentID)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		peer := &Peer{
			ID:        agentID,
			Role:      role,
			Status:    status,
			IsLiaison: strings.EqualFold(agentID, currentLiaison),
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

// SetStatus sets the 300s ephemeral status message for this agent
func (c *Client) SetStatus(ctx context.Context, statusText string) error {
	statusKey := StatusKey(c.cfg.AgentID)
	_, err := c.Exec(ctx, "SET", statusKey, statusText, "EX", strconv.Itoa(StatusTTLSeconds))
	return err
}

// GetStatus retrieves the status message for a given agent
func (c *Client) GetStatus(ctx context.Context, agentID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	statusKey := StatusKey(agentID)
	val, err := c.Exec(ctx, "GET", statusKey)
	if err != nil {
		return "", err
	}
	if val.IsNull {
		return "", nil
	}
	return val.AsString()
}

// SetFinger stores profile and role metadata for this agent
func (c *Client) SetFinger(ctx context.Context, info map[string]string) error {
	fingerKey := FingerKey(c.cfg.AgentID)
	var args []string
	args = append(args, fingerKey)
	for k, v := range info {
		args = append(args, k, v)
	}
	_, err := c.Exec(ctx, "HSET", args...)
	return err
}

// GetFinger retrieves profile metadata for an agent
func (c *Client) GetFinger(ctx context.Context, agentID string) (map[string]string, error) {
	fingerKey := FingerKey(agentID)
	val, err := c.Exec(ctx, "HGETALL", fingerKey)
	if err != nil || val.IsNull {
		return nil, err
	}
	m, err := val.AsMap()
	if err != nil {
		return nil, err
	}
	res := make(map[string]string, len(m))
	for k, v := range m {
		res[k] = v.String()
	}
	return res, nil
}

// ParkBlob stores a large payload into a 24-hour TTL key (<id>:blob:<ts>)
func (c *Client) ParkBlob(ctx context.Context, data []byte) (string, error) {
	ts := time.Now().UnixMilli()
	blobKey := BlobKey(c.cfg.AgentID, ts)
	_, err := c.Exec(ctx, "SET", blobKey, string(data), "EX", strconv.Itoa(BlobTTLSeconds))
	if err != nil {
		return "", err
	}
	return blobKey, nil
}

// GetBlob retrieves a parked blob by key
func (c *Client) GetBlob(ctx context.Context, blobKey string) ([]byte, error) {
	val, err := c.Exec(ctx, "GET", blobKey)
	if err != nil || val.IsNull {
		return nil, fmt.Errorf("blob %s not found", blobKey)
	}
	str, err := val.AsString()
	if err != nil {
		return nil, err
	}
	return []byte(str), nil
}

// SetLiaison appoints an agent as the current fleet liaison
func (c *Client) SetLiaison(ctx context.Context, agentID string) error {
	_, err := c.Exec(ctx, "SET", KeyLiaisonCurrent, strings.ToLower(agentID))
	return err
}

// GetLiaison gets the current appointed liaison agent
func (c *Client) GetLiaison(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	val, err := c.Exec(ctx, "GET", KeyLiaisonCurrent)
	if err != nil {
		return "", err
	}
	if val.IsNull {
		return "", nil
	}
	return val.AsString()
}

// GetPollInterval reads poll-interval key from Valkey, returns default 60 if unset or invalid (valid range: 5 to 3600)
func (c *Client) GetPollInterval(ctx context.Context) (int, error) {
	val, err := c.Exec(ctx, "GET", KeyPollInterval)
	if err != nil {
		return DefaultPollInterval, err
	}
	if val.IsNull {
		return DefaultPollInterval, nil
	}
	str, err := val.AsString()
	if err != nil {
		return DefaultPollInterval, nil
	}
	secs, err := strconv.Atoi(strings.TrimSpace(str))
	if err != nil || secs < MinPollInterval || secs > MaxPollInterval {
		return DefaultPollInterval, nil
	}
	return secs, nil
}

// SetPollInterval sets the shared poll interval in Valkey (valid range: 5 to 3600)
func (c *Client) SetPollInterval(ctx context.Context, seconds int) error {
	if seconds < MinPollInterval || seconds > MaxPollInterval {
		return fmt.Errorf("poll interval must be between %d and %d seconds, got %d", MinPollInterval, MaxPollInterval, seconds)
	}
	_, err := c.Exec(ctx, "SET", KeyPollInterval, strconv.Itoa(seconds))
	return err
}

// RegisterIdentity publishes an identity attestation and public key
func (c *Client) RegisterIdentity(ctx context.Context, rec IdentityRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	idKey := IdentityKey(rec.Name)
	_, err = c.Exec(ctx, "SET", idKey, string(data))
	return err
}

// GetIdentity retrieves an identity record and caches its public key
func (c *Client) GetIdentity(ctx context.Context, id string) (*IdentityRecord, error) {
	idKey := IdentityKey(id)
	val, err := c.Exec(ctx, "GET", idKey)
	if err != nil || val.IsNull {
		return nil, fmt.Errorf("identity %s not found", id)
	}
	raw, err := val.AsString()
	if err != nil {
		return nil, err
	}
	rec, err := ParseIdentityRecord(raw)
	if err != nil {
		return nil, err
	}

	if rec.PubKey != "" {
		pub, err := DecodePublicKeyBase64(rec.PubKey)
		if err == nil {
			c.pubKeysMu.Lock()
			c.pubKeys[strings.ToLower(id)] = pub
			c.pubKeysMu.Unlock()
		}
	}
	return rec, nil
}

// VerifyMessage verifies the cryptographic signature on a message.
// If skipFreshness is true, timestamp freshness checks (5m future / 24h past) are bypassed.
func (c *Client) VerifyMessage(ctx context.Context, msg *Message, skipFreshness ...bool) bool {
	if msg == nil || !msg.IsSigned || msg.Signature == "" {
		return false
	}

	skip := len(skipFreshness) > 0 && skipFreshness[0]
	if !skip {
		now := time.Now().UnixMilli()
		diff := now - msg.Timestamp
		if diff < -300000 || diff > 86400000 { // > 5 minutes in future or > 24 hours old
			return false
		}
	}

	sender := strings.ToLower(msg.Sender)
	c.pubKeysMu.RLock()
	pub, found := c.pubKeys[sender]
	c.pubKeysMu.RUnlock()

	if !found {
		rec, err := c.GetIdentity(ctx, sender)
		if err != nil || rec.PubKey == "" {
			return false
		}
		c.pubKeysMu.RLock()
		pub = c.pubKeys[sender]
		c.pubKeysMu.RUnlock()
	}

	if pub == nil {
		return false
	}

	valid := VerifyPayload(pub, msg.SigningPayload(), msg.Signature)
	msg.IsVerified = valid
	return valid
}

func (c *Client) parseXReadResponse(ctx context.Context, val resp.Value) ([]*Message, error) {
	streamList, err := val.AsArray()
	if err != nil {
		return nil, err
	}

	var messages []*Message

	for _, streamEntry := range streamList {
		pair, err := streamEntry.AsArray()
		if err != nil || len(pair) < 2 {
			continue
		}
		streamName := pair[0].String()
		entries, err := pair[1].AsArray()
		if err != nil {
			continue
		}

		var lastID string
		for _, entryVal := range entries {
			entryPair, err := entryVal.AsArray()
			if err != nil || len(entryPair) < 2 {
				continue
			}
			msgID := entryPair[0].String()
			fields, err := entryPair[1].AsMap()
			if err != nil {
				continue
			}

			fieldMap := make(map[string]string, len(fields))
			for k, v := range fields {
				fieldMap[k] = v.String()
			}

			msg := MessageFromFields(msgID, fieldMap)
			c.VerifyMessage(ctx, msg)
			messages = append(messages, msg)
			lastID = msgID
		}

		if lastID != "" {
			_ = c.SetCursor(ctx, streamName, lastID)
		}
	}

	return messages, nil
}

// LoadClientFromEnv builds a ClientConfig from standard backplane environment variables
func LoadClientFromEnv() ClientConfig {
	profile := os.Getenv("BP_PROFILE")
	if profile == "" {
		profile = "default"
	}
	cfg, err := LoadClientWithProfile(profile)
	if err != nil {
		// Fallback to legacy env loading if profile file does not exist
		return loadClientDirectEnv()
	}
	return cfg
}

// LoadClientWithProfile loads client configuration from ~/.local/state/bp/profiles/<profile>.env
// falling back to legacy environment loading if the profile is "default" and the file does not exist.
func LoadClientWithProfile(profileName string) (ClientConfig, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = "default"
	}

	// 1. Check fail-closed condition for default profile: BP_MODE=human conflicting with explicit BP_AGENT
	if profileName == "default" {
		mode := strings.ToLower(os.Getenv("BP_MODE"))
		agent := os.Getenv("BP_AGENT")
		if mode == "human" && agent != "" && agent != os.Getenv("HUMAN_NAME") && agent != "operator" {
			return ClientConfig{}, fmt.Errorf("conflicting configuration: cannot operate in human operator mode while BP_AGENT is set to %q", agent)
		}
	}

	// 2. Resolve profile file in ~/.local/state/bp/profiles/<profile>.env
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	xdgState := os.Getenv("XDG_STATE_HOME")
	if xdgState == "" {
		xdgState = filepath.Join(home, ".local", "state")
	}
	profilePath := filepath.Join(xdgState, "bp", "profiles", fmt.Sprintf("%s.env", profileName))

	data, err := os.ReadFile(profilePath)
	if err != nil {
		if profileName == "default" {
			return loadClientDirectEnv(), nil
		}
		return ClientConfig{}, fmt.Errorf("profile %q not found at %s", profileName, profilePath)
	}

	// Parse key-value lines
	envMap := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if (strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"")) ||
				(strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'")) {
				if len(v) >= 2 {
					v = v[1 : len(v)-1]
				}
			}
			envMap[k] = v
		}
	}

	// Build config using profile values.
	// For named profiles (non-default), values must come strictly from the profile file
	// and MUST NOT inherit ambient identity/mode env vars (BP_MODE, BP_AGENT, AGENT_NAME,
	// HUMAN_NAME, BP_PASSWORD, AGENT_PASSWORD, HUMAN_BACKPLANE_PASSWORD, BP_SIGNING_KEY).
	// Only host/port infrastructure connection settings may fall back to ambient env.
	getVal := func(key string) string {
		var val string
		if profileName != "default" {
			if v, ok := envMap[key]; ok && v != "" {
				val = v
			} else if key == "BP_HOST" || key == "BP_PORT" {
				val = os.Getenv(key)
			}
		} else {
			if v := os.Getenv(key); v != "" {
				val = v
			} else {
				val = envMap[key]
			}
		}
		val = strings.TrimSpace(val)
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
			(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			if len(val) >= 2 {
				val = val[1 : len(val)-1]
			}
		}
		return val
	}

	host := getVal("BP_HOST")
	if host == "" {
		host = "localhost"
	}
	port := 6379
	if portStr := getVal("BP_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	rawMode := strings.ToLower(getVal("BP_MODE"))
	var username, password, agentID, resolvedMode string

	isAgent := getVal("BP_AGENT") != "" || getVal("AGENT_NAME") != "" || getVal("BP_PASSWORD") != ""
	if rawMode == "human" || (rawMode == "" && !isAgent && getVal("HUMAN_BACKPLANE_PASSWORD") != "") {
		username = getVal("HUMAN_NAME")
		if username == "" {
			username = "operator"
		}
		password = getVal("HUMAN_BACKPLANE_PASSWORD")
		agentID = username
		resolvedMode = "human"
	} else {
		agentID = getVal("BP_AGENT")
		if agentID == "" {
			agentID = getVal("AGENT_NAME")
		}
		username = agentID
		password = getVal("BP_PASSWORD")
		if password == "" {
			password = getVal("AGENT_PASSWORD")
		}
		resolvedMode = "agent"
	}

	signingKeyPEM := getVal("BP_SIGNING_KEY_PEM")
	var signingKey ed25519.PrivateKey
	if keyPath := getVal("BP_SIGNING_KEY"); keyPath != "" {
		priv, err := LoadPrivateKeyFromFile(keyPath)
		if err != nil {
			return ClientConfig{}, fmt.Errorf("failed loading signing key from %s: %w", keyPath, err)
		}
		signingKey = priv
	} else if agentID != "" {
		xdgData := os.Getenv("XDG_DATA_HOME")
		if xdgData == "" {
			xdgData = filepath.Join(home, ".local", "share")
		}
		defaultKey := filepath.Join(xdgData, "agent-sandbox", "secrets", strings.ToLower(agentID), "signing-key.pem")
		if priv, err := LoadPrivateKeyFromFile(defaultKey); err == nil {
			signingKey = priv
		}
	}

	return ClientConfig{
		Host:          host,
		Port:          port,
		Username:      username,
		Password:      password,
		AgentID:       agentID,
		Mode:          resolvedMode,
		SigningKey:    signingKey,
		SigningKeyPEM: signingKeyPEM,
	}, nil
}

func loadClientDirectEnv() ClientConfig {
	host := os.Getenv("BP_HOST")
	if host == "" {
		host = "localhost"
	}

	port := 6379
	if portStr := os.Getenv("BP_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	rawMode := strings.ToLower(os.Getenv("BP_MODE"))
	var username, password, resolvedMode string
	var agentID string

	isAgent := os.Getenv("BP_AGENT") != "" || os.Getenv("AGENT_NAME") != "" || os.Getenv("BP_PASSWORD") != ""
	if rawMode == "human" || (rawMode == "" && !isAgent && os.Getenv("HUMAN_BACKPLANE_PASSWORD") != "") {
		username = os.Getenv("HUMAN_NAME")
		if username == "" {
			username = "operator"
		}
		password = os.Getenv("HUMAN_BACKPLANE_PASSWORD")
		agentID = username
		resolvedMode = "human"
	} else {
		agentID = os.Getenv("BP_AGENT")
		if agentID == "" {
			agentID = os.Getenv("AGENT_NAME")
		}
		username = agentID
		password = os.Getenv("BP_PASSWORD")
		if password == "" {
			password = os.Getenv("AGENT_PASSWORD")
		}
		resolvedMode = "agent"
	}

	signingKeyPEM := os.Getenv("BP_SIGNING_KEY_PEM")
	var signingKey ed25519.PrivateKey
	if keyPath := os.Getenv("BP_SIGNING_KEY"); keyPath != "" {
		if priv, err := LoadPrivateKeyFromFile(keyPath); err == nil {
			signingKey = priv
		}
	} else if agentID != "" {
		home, _ := os.UserHomeDir()
		defaultKey := filepath.Join(home, ".local", "share", "agent-sandbox", "secrets", strings.ToLower(agentID), "signing-key.pem")
		if priv, err := LoadPrivateKeyFromFile(defaultKey); err == nil {
			signingKey = priv
		}
	}

	return ClientConfig{
		Host:          host,
		Port:          port,
		Username:      username,
		Password:      password,
		AgentID:       agentID,
		Mode:          resolvedMode,
		SigningKey:    signingKey,
		SigningKeyPEM: signingKeyPEM,
	}
}
