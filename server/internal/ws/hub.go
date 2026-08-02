package ws

import (
	"encoding/json"
	"log"
	"sync/atomic"
)

// historyLimit caps the retained frames. The buffer is bounded so memory
// stays capped; a client connecting mid-stream gets this much backfill
// rather than an empty chart.
const historyLimit = 300

// Hub owns the set of connected clients and fans messages out to them.
// One Hub runs for the lifetime of the process — it is not per-connection.
type Hub struct {
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte

	// clients and history are owned by Run's goroutine and are never
	// touched from anywhere else, so they need no synchronization.
	// clientCount mirrors len(clients) for readers outside that goroutine.
	clients     map[*Client]struct{}
	history     [][]byte
	clientCount atomic.Int64
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
			h.clients[c] = struct{}{}
			h.clientCount.Store(int64(len(h.clients)))

			// Backfill the new client with recent frames.
			for _, frame := range h.history {
				select {
				case c.send <- frame:
				default:
					// Already behind before it started; skip the rest of
					// the backfill rather than block the hub.
				}
			}

		case c := <-h.unregister:
			h.drop(c)

		case msg := <-h.broadcast:
			h.history = append(h.history, msg)
			if len(h.history) > historyLimit {
				h.history = h.history[1:]
			}

			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Slow client: drop it rather than stall every other
					// client and the sampler behind it.
					h.drop(c)
				}
			}
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
	return int(h.clientCount.Load())
}

// drop removes a client and closes its send channel. Run-goroutine only.
func (h *Hub) drop(c *Client) {
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	close(c.send)
	h.clientCount.Store(int64(len(h.clients)))
}
