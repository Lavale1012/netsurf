package routes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/lavale1012/net-monitor/server/internal/api/helpers/network"
)

// RegisterNetworkRoutes mounts the network routes under the given group.
func RegisterNetworkRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/network")
	g.GET("/connections", listConnections)
}

func listConnections(c *gin.Context) {
	connections, err := network.GetConnections()
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
