// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbpd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

type mockStreamReceiver struct {
	mu           sync.Mutex
	recvCalled   int
	blockSecs    []int
	messages     [][]*libbp.Message
	errs         []error
	callIdx      int
	blockOnCalls bool
}

func (m *mockStreamReceiver) Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error) {
	m.mu.Lock()
	m.recvCalled++
	m.blockSecs = append(m.blockSecs, blockSeconds)
	idx := m.callIdx
	m.callIdx++
	var msgs []*libbp.Message
	var err error
	if idx < len(m.messages) {
		msgs = m.messages[idx]
	}
	if idx < len(m.errs) {
		err = m.errs[idx]
	}
	block := m.blockOnCalls
	m.mu.Unlock()

	if block {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}

	return msgs, err
}

func TestStreamListenerCreation(t *testing.T) {
	rec := &mockStreamReceiver{}
	l := NewStreamListener(rec, 0)
	if l.BlockSeconds() != 5 {
		t.Errorf("expected default blockSec 5, got %d", l.BlockSeconds())
	}

	l2 := NewStreamListener(rec, 12)
	if l2.BlockSeconds() != 12 {
		t.Errorf("expected blockSec 12, got %d", l2.BlockSeconds())
	}

	l2.SetBackoff(50 * time.Millisecond)
	if l2.backoff != 50*time.Millisecond {
		t.Errorf("expected backoff 50ms, got %v", l2.backoff)
	}
	l2.SetBackoff(0)
	if l2.backoff != 50*time.Millisecond {
		t.Errorf("expected backoff unchanged when <= 0, got %v", l2.backoff)
	}
}

func TestStreamListenerValidation(t *testing.T) {
	ctx := context.Background()
	lNil := NewStreamListener(nil, 5)
	if err := lNil.Listen(ctx, func(_ context.Context, _ *libbp.Message) error { return nil }); err == nil {
		t.Fatalf("expected error when receiver is nil")
	}

	rec := &mockStreamReceiver{}
	l := NewStreamListener(rec, 5)
	if err := l.Listen(ctx, nil); err == nil {
		t.Fatalf("expected error when handler is nil")
	}
}

func TestStreamListenerListen(t *testing.T) {
	rec := &mockStreamReceiver{
		messages: [][]*libbp.Message{
			{
				{ID: "1-0", Sender: "alice", Content: "hello"},
				{ID: "2-0", Sender: "bob", Content: "world"},
			},
			nil,
		},
		errs: []error{
			nil,
			errors.New("transient error"),
		},
		blockOnCalls: true,
	}

	l := NewStreamListener(rec, 3)
	l.SetBackoff(5 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var received []*libbp.Message
	var mu sync.Mutex
	err := l.Listen(ctx, func(_ context.Context, msg *libbp.Message) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, msg)
		if msg.Content == "world" {
			return errors.New("handler error")
		}
		return nil
	})

	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected listen error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Errorf("expected 2 received messages, got %d", len(received))
	}
	if len(received) > 0 && received[0].Content != "hello" {
		t.Errorf("expected first message 'hello', got %s", received[0].Content)
	}
}

func TestStreamListenerListenContextCanceledEarly(t *testing.T) {
	rec := &mockStreamReceiver{}
	l := NewStreamListener(rec, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before listen starts

	err := l.Listen(ctx, func(_ context.Context, _ *libbp.Message) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
