package routes

import (
	"log"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/lavale1012/net-monitor/server/internal/api/handlers"
	"github.com/lavale1012/net-monitor/server/internal/ws"
)

// RegisterWSRoutes mounts the live stream under the given group.
//
// allowedOrigins is checked on upgrade: browsers do not apply CORS to
// WebSocket handshakes, so without this any page on the internet could
// open a socket to a local dashboard and read the machine's traffic.
func RegisterWSRoutes(rg *gin.RouterGroup, hub *ws.Hub, allowedOrigins []string) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     originChecker(allowedOrigins),
	}

	rg.GET("/live-packets", func(c *gin.Context) {
		handlers.LivePacketStream(c, hub, upgrader)
	})
}

func originChecker(allowed []string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Non-browser client (curl, websocat, a Go test). No origin to
			// forge, so nothing to defend against here.
			return true
		}
		if slices.Contains(allowed, "*") || slices.Contains(allowed, origin) {
			return true
		}
		log.Printf("ws: rejected origin %q", origin)
		return false
	}
}
