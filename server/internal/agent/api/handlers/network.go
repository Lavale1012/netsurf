package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lavale1012/net-monitor/server/internal/agent/network"
	"github.com/lavale1012/net-monitor/server/internal/ws"
)

// Package handlers holds the HTTP handlers for each route group. Each is
// thin: call the matching Collect* function and translate its result into a
// status code and the {"data": ...} envelope.
//
// Handlers are kept out of agent/network so that package stays free of
// gin. The WebSocket samplers call the same collectors, and a sampler has
// no HTTP request to fail — it needs a plain error it can turn into an
// error frame.

// ListConnections serves the live connection list.
func ListConnections(c *gin.Context) {
	connections, err := network.CollectConnections()
	if err != nil {
		if errors.Is(err, network.ErrConnectionsUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"detail": "needs elevated privileges: run the server under sudo",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": connections})
}

// GetThroughput serves the current up/down rate.
func GetThroughput(c *gin.Context) {
	throughput, err := network.CollectThroughput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": throughput})
}

// GetHostnames serves the reverse-DNS names resolved so far.
func GetHostnames(c *gin.Context) {
	hostnames, err := network.CollectHostnames()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": hostnames})
}

// GetApps serves per-application rollups.
func GetApps(c *gin.Context) {
	apps, err := network.CollectApps()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": apps})
}

// LivePacketStream upgrades the request to a WebSocket and hands the
// connection to the hub.
//
// It deliberately does nothing else. The hub pushes frames to every
// attached client, and the sampler is what produces them — this handler
// only has to get the socket registered.
//
// In particular it must NOT call network.GetLivePackets: that drains the
// capture accumulator and returns the delta, so calling it here would
// steal flows the sampler is about to broadcast. And it must not write a
// JSON body: after a successful Upgrade the response is already 101
// Switching Protocols, and the connection belongs to the WebSocket.
func LivePacketStream(c *gin.Context, hub *ws.Hub, upgrader websocket.Upgrader) {

	// One pcap handle is opened at startup and shared by every client, so a
	// request for a different interface cannot be served. Reject it rather
	// than accept the parameter and stream the wrong device anyway.
	//
	// This must happen before the upgrade: once the handshake completes the
	// response is 101 Switching Protocols and there is no way to report an
	// HTTP error.
	if dev := c.Query("device"); dev != "" && dev != network.CaptureDevice() {
		c.JSON(http.StatusBadRequest, gin.H{
			"detail": "capture is running on " + network.CaptureDevice() + ", not " + dev,
		})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		log.Printf("ws: upgrade: %v", err)
		return
	}

	// Returns immediately; the read/write pumps run in their own goroutines.
	// Frames come from the samplers via the hub — this handler does not
	// produce data, and must not call GetLivePackets (that drains the
	// accumulator the packets sampler is about to broadcast).
	ws.Serve(hub, conn)
}
