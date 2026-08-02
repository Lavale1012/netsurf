package ws

import (
	"context"
	"log"
	"time"
)

// Source produces one sample. An error means the source is currently
// unavailable — which the stream reports explicitly, because "no data"
// and "cannot read" are different states and a dashboard showing an
// empty list for both is indistinguishable from a healthy idle machine.
type Source func() (any, error)

// Sampler drives one Source on a fixed interval and broadcasts each
// result to the hub. It samples the machine, so exactly one runs
// regardless of how many dashboard clients are attached — a sampler that
// stopped with the last client would lose the history the buffer exists
// to retain.
type Sampler struct {
	Hub      *Hub
	Interval time.Duration
	Type     string
	Source   Source

	// lastErr is the most recent failure, used to emit an error frame on
	// transition rather than every tick. Owned by Run's goroutine.
	lastErr string
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
				// Only on transition: a persistently failing source would
				// otherwise fill its history with identical error frames,
				// evicting the last good data a new client backfills from.
				if msg := err.Error(); msg != s.lastErr {
					s.lastErr = msg
					log.Printf("ws: sampler %q: %v", s.Type, err)
					s.Hub.Broadcast(Frame{
						Type:  s.Type,
						TS:    time.Now().UnixMilli(),
						Error: msg,
					})
				}
				continue
			}

			// A data frame carries no Error, which is how the client sees
			// a source recover.
			s.lastErr = ""
			s.Hub.Broadcast(Frame{
				Type: s.Type,
				TS:   time.Now().UnixMilli(),
				Data: data,
			})
		}
	}
}
