package ws

import (
	"context"
	"log"
	"time"
)

// Frame is the envelope every broadcast is wrapped in, so the client can
// switch on Type as more stream kinds are added (packets, throughput,
// per-app rollups).
type Frame struct {
	Type string `json:"type"`
	TS   int64  `json:"ts"` // Unix milliseconds
	Data any    `json:"data"`
}

// Source produces one sample. Returning an error logs and skips the tick
// rather than killing the sampler — a transient failure should not end
// the stream.
type Source func() (any, error)

// Sampler drives one Source on a fixed interval and broadcasts each
// result to the hub. It samples the machine, so exactly one runs
// regardless of how many dashboard clients are attached — a sampler that
// stopped with the last client would lose the history the ring buffer
// exists to retain.
type Sampler struct {
	Hub      *Hub
	Interval time.Duration
	Type     string
	Source   Source
}

// Run samples until ctx is cancelled. Start it once, in a goroutine, at
// application startup.
func (s *Sampler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("ws: sampler %q stopped", s.Type)
			return

		case <-ticker.C:
			data, err := s.Source()
			if err != nil {
				log.Printf("ws: sampler %q: %v", s.Type, err)
				continue
			}
			s.Hub.Broadcast(Frame{
				Type: s.Type,
				TS:   time.Now().UnixMilli(),
				Data: data,
			})
		}
	}
}
