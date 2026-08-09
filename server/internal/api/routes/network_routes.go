package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/lavale1012/net-monitor/server/internal/api/handlers"
)

// RegisterNetworkRoutes mounts the network routes under the given group.
//
// This file owns the URL surface only. Handlers live in api/handlers, and
// the collection logic they call lives in api/helpers/network.
func RegisterNetworkRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/network")
	g.GET("/connections", handlers.ListConnections)
	g.GET("/throughput", handlers.GetThroughput)
	g.GET("/hostnames", handlers.GetHostnames)
	g.GET("/apps", handlers.GetApps)
}
