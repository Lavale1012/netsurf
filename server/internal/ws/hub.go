package ws

import (
	"encoding/json"
	"log"
	"sync"
)

// historyLimit caps the retained frames. The ring buffer is bounded so
// memory stays capped; a client connecting mid-stream gets this much
// backfill rather than an empty chart.
const historyLimit = 300

// Hub owns the set of connected clients and fans messages out to them.
// One Hub runs for the lifetime of the process — it is not per-connection.
type Hub struct {
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte

	mu      sync.RWMutex
	clients map[*Client]struct{}
	history [][]byte
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 64),
		clients:    make(map[*Client]struct{}),
		history:    make([][]byte, 0, historyLimit),
	}
}

// Run processes hub events. Start it once, in a goroutine, at startup.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()

			// Backfill the new client with recent frames.
			for _, frame := range h.snapshotHistory() {
				select {
				case c.send <- frame:
				default:
					// Already behind before it started; skip the rest of
					// the backfill rather than block the hub.
				}
			}

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.appendHistory(msg)

			h.mu.Lock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Slow client: drop it rather than stall every other
					// client and the sampler behind it.
					delete(h.clients, c)
					close(c.send)
				}
			}
			h.mu.Unlock()
		}
	}
}

// Broadcast marshals v as JSON and sends it to every connected client.
// Safe to call from any goroutine. Never blocks the caller.
func (h *Hub) Broadcast(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		log.Printf("ws: marshal frame: %v", err)
		return
	}
	select {
	case h.broadcast <- payload:
	default:
		log.Printf("ws: broadcast buffer full, dropping frame")
	}
}

// ClientCount reports how many clients are currently attached.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) appendHistory(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.history) == historyLimit {
		copy(h.history, h.history[1:])
		h.history[len(h.history)-1] = msg
		return
	}
	h.history = append(h.history, msg)
}

func (h *Hub) snapshotHistory() [][]byte {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([][]byte, len(h.history))
	copy(out, h.history)
	return out
}
