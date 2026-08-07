package routes

import (
	"github.com/gin-gonic/gin"

	// Go resolves packages by module path, never by file path — there is no
	// relative import form. "helpers" here is a local alias for the package,
	// whose real name is "network".
	helpers "github.com/lavale1012/net-monitor/server/internal/api/helpers/network"
)

// RegisterNetworkRoutes mounts the network routes under the given group.
//
// Handlers here are thin by design: they call a collector in
// helpers/network and translate its error into a status code. Collection
// logic lives in the helper so the WebSocket samplers can reuse it — they
// have no HTTP request to fail.
func RegisterNetworkRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/network")
	g.GET("/connections", helpers.ListConnections)
	g.GET("/throughput", helpers.GetThroughput)
	g.GET("/hostnames", helpers.GetHostnames)
	g.GET("/apps", helpers.GetApps)
}
