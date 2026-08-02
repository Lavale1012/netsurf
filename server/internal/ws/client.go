package ws

import (
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait is how long a single write may take before it is abandoned.
	writeWait = 10 * time.Second
	// pongWait is how long to wait for a pong before considering the peer dead.
	pongWait = 60 * time.Second
	// pingPeriod must be shorter than pongWait so a ping always precedes the
	// deadline it is meant to satisfy.
	pingPeriod = (pongWait * 9) / 10
	// maxMessageSize caps inbound frames. The dashboard is read-only, so
	// anything large is either a mistake or hostile.
	maxMessageSize = 4096
	// sendBuffer is the per-client outbound queue depth. A client that falls
	// this far behind is dropped rather than allowed to stall the hub.
	sendBuffer = 64
)

// Client is one connected dashboard socket.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// Serve registers conn with the hub and runs its pumps. It returns
// immediately; the pumps run in their own goroutines.
func Serve(h *Hub, conn *websocket.Conn) {
	c := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, sendBuffer),
	}
	h.register <- c

	go c.writePump()
	go c.readPump()
}

// readPump drains inbound frames and keeps the read deadline fresh.
// The dashboard never sends anything meaningful, but reading is required
// for the connection to process control frames (pong, close).
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws: read: %v", err)
			}
			return
		}
	}
}

// writePump sends queued frames and pings on an interval.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel — the client was dropped.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
