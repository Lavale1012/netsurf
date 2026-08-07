package network

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// HTTP handlers for the network routes. Each is thin: call the matching
// Collect* function and translate its result into a status code and the
// {"data": ...} envelope.
//
// The Collect* functions stay separate from these handlers because the
// WebSocket samplers call them too, and a sampler has no HTTP request to
// fail — it needs a plain error it can turn into an error frame.

// ListConnections serves the live connection list.
func ListConnections(c *gin.Context) {
	connections, err := CollectConnections()
	if err != nil {
		if errors.Is(err, ErrConnectionsUnavailable) {
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
	throughput, err := CollectThroughput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": throughput})
}

// GetHostnames serves the reverse-DNS names resolved so far.
func GetHostnames(c *gin.Context) {
	hostnames, err := CollectHostnames()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": hostnames})
}

// GetApps serves per-application rollups.
func GetApps(c *gin.Context) {
	apps, err := CollectApps()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": apps})
}
