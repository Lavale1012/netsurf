package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient registers a client with a buffer large enough that the
// hub never drops it mid-test.
func newTestClient(t *testing.T, h *Hub, buf int) *Client {
	t.Helper()
	c := &Client{hub: h, send: make(chan []byte, buf)}
	h.register <- c
	// The send above only guarantees Run received the client; the backfill
	// it then performs happens on Run's goroutine.
	time.Sleep(30 * time.Millisecond)
	return c
}

// settle waits for the hub to drain queued broadcasts.
func settle(h *Hub) {
	for len(h.broadcast) > 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
}

// collect reads everything currently queued for c, grouped by frame type.
func collect(c *Client) map[string][]Frame {
	out := make(map[string][]Frame)
	for {
		select {
		case payload := <-c.send:
			var f Frame
			if err := json.Unmarshal(payload, &f); err != nil {
				continue
			}
			out[f.Type] = append(out[f.Type], f)
		default:
			return out
		}
	}
}

// A fast stream must not evict a slow stream's frames from the backfill.
func TestHistoryIsBucketedPerType(t *testing.T) {
	h := NewHub()
	go h.Run()

	// Flood "fast" well past the per-type cap, interleaving a handful of
	// "slow" frames — the shape of a 1s sampler running beside a 30s one.
	for i := 0; i < historyLimit+100; i++ {
		h.Broadcast(Frame{Type: "fast", TS: int64(i), Data: i})
		if i%100 == 0 {
			h.Broadcast(Frame{Type: "slow", TS: int64(i), Data: i})
			settle(h)
		}
	}
	settle(h)

	got := collect(newTestClient(t, h, 4096))

	if n := len(got["fast"]); n != historyLimit {
		t.Errorf("fast stream: got %d frames, want %d (per-type cap)", n, historyLimit)
	}
	// 4 slow frames were sent (i = 0, 100, 200, 300); all must survive the
	// flood. Under one shared buffer they would have been evicted.
	if n := len(got["slow"]); n != 4 {
		t.Errorf("slow stream: got %d frames, want 4 — evicted by the fast stream", n)
	}
}

// A failing Source must surface as an error frame, not silence.
func TestSamplerBroadcastsErrorFrame(t *testing.T) {
	h := NewHub()
	go h.Run()
	c := newTestClient(t, h, 256)

	s := &Sampler{
		Hub:      h,
		Interval: 10 * time.Millisecond,
		Type:     "conns",
		Source:   func() (any, error) { return nil, errors.New("needs elevated privileges") },
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	time.Sleep(150 * time.Millisecond) // ~15 ticks
	cancel()
	settle(h)

	frames := collect(c)["conns"]
	if len(frames) == 0 {
		t.Fatal("failing source produced no frames — the dashboard would see silence")
	}
	// Emitted on transition, not per tick: ~15 ticks must not mean 15 frames.
	if len(frames) != 1 {
		t.Errorf("got %d error frames over ~15 ticks, want 1 (transition only)", len(frames))
	}
	if frames[0].Error != "needs elevated privileges" {
		t.Errorf("Error = %q, want the source's message", frames[0].Error)
	}
}

// Recovering must produce data frames again, with no Error set.
func TestSamplerRecovers(t *testing.T) {
	h := NewHub()
	go h.Run()
	c := newTestClient(t, h, 256)

	// atomic: written here, read from the sampler's goroutine.
	var failing atomic.Bool
	failing.Store(true)

	s := &Sampler{
		Hub:      h,
		Interval: 10 * time.Millisecond,
		Type:     "conns",
		Source: func() (any, error) {
			if failing.Load() {
				return nil, errors.New("boom")
			}
			return []string{"ok"}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	time.Sleep(60 * time.Millisecond)
	failing.Store(false)
	time.Sleep(60 * time.Millisecond)
	cancel()
	settle(h)

	frames := collect(c)["conns"]
	var sawErr, sawData bool
	for _, f := range frames {
		if f.Error != "" {
			sawErr = true
		}
		if f.Error == "" && f.Data != nil {
			sawData = true
		}
	}
	if !sawErr {
		t.Error("never saw the initial error frame")
	}
	if !sawData {
		t.Error("never saw a data frame after recovery")
	}
}
