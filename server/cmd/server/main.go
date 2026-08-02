package main

import (
	"log"
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/lavale1012/net-monitor/server/internal/api/routes"
	"github.com/lavale1012/net-monitor/server/internal/core"
)

func main() {
	settings := core.Load()

	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     settings.CORSOrigins,
		AllowMethods:     []string{"*"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: true,
	}))

	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"Hello": "World"})
	})
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group(settings.APIPrefix)
	routes.RegisterUserRoutes(api)
	routes.RegisterNetworkRoutes(api)

	addr := "127.0.0.1:" + settings.Port
	log.Printf("%s listening on http://%s", settings.AppName, addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
