package ws

import (
	"encoding/json"
	"log"
	"sync/atomic"
)

// historyLimit caps the retained frames *per stream type*, so a fast
// sampler cannot crowd a slow one out of a new client's backfill.
const historyLimit = 300

// Frame is the envelope every broadcast is wrapped in. Type lets the
// client route a frame to the right stream; Error reports that the
// stream's source is failing, which is distinct from it having no data.
type Frame struct {
	Type  string `json:"type"`
	TS    int64  `json:"ts"` // Unix milliseconds
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// queued is a marshalled frame tagged with its type, so the hub can
// bucket history without re-parsing the JSON.
type queued struct {
	typ     string
	payload []byte
}

// Hub owns the set of connected clients and fans messages out to them.
// One Hub runs for the lifetime of the process — it is not per-connection.
type Hub struct {
	register   chan *Client
	unregister chan *Client
	broadcast  chan queued

	// clients and history are owned by Run's goroutine and are never
	// touched from anywhere else, so they need no synchronization.
	// clientCount mirrors len(clients) for readers outside that goroutine.
	clients     map[*Client]struct{}
	history     map[string][][]byte
	clientCount atomic.Int64
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan queued, 64),
		clients:    make(map[*Client]struct{}),
		history:    make(map[string][][]byte),
	}
}

// Run processes hub events. Start it once, in a goroutine, at startup.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = struct{}{}
			h.clientCount.Store(int64(len(h.clients)))
			h.backfill(c)

		case c := <-h.unregister:
			h.drop(c)

		case q := <-h.broadcast:
			hist := append(h.history[q.typ], q.payload)
			if len(hist) > historyLimit {
				hist = hist[1:]
			}
			h.history[q.typ] = hist

			for c := range h.clients {
				select {
				case c.send <- q.payload:
				default:
					// Slow client: drop it rather than stall every other
					// client and the sampler behind it.
					h.drop(c)
				}
			}
		}
	}
}

// Broadcast marshals f and sends it to every connected client, retaining
// it in f.Type's history. Safe to call from any goroutine; never blocks.
func (h *Hub) Broadcast(f Frame) {
	payload, err := json.Marshal(f)
	if err != nil {
		log.Printf("ws: marshal %q frame: %v", f.Type, err)
		return
	}
	select {
	case h.broadcast <- queued{typ: f.Type, payload: payload}:
	default:
		log.Printf("ws: broadcast buffer full, dropping %q frame", f.Type)
	}
}

// ClientCount reports how many clients are currently attached.
func (h *Hub) ClientCount() int {
	return int(h.clientCount.Load())
}

// backfill replays retained frames to a newly connected client. Frames
// are ordered within each stream; streams themselves interleave, which
// is fine because the client routes on Type. Run-goroutine only.
func (h *Hub) backfill(c *Client) {
	for _, frames := range h.history {
		for _, payload := range frames {
			select {
			case c.send <- payload:
			default:
				// Already behind before it started — abandon the rest of
				// the backfill rather than block the hub on a client that
				// cannot keep up.
				return
			}
		}
	}
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
