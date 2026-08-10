package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/lavale1012/net-monitor/server/internal/agent/api/routes"
	"github.com/lavale1012/net-monitor/server/internal/agent/network"
	"github.com/lavale1012/net-monitor/server/internal/core"
	"github.com/lavale1012/net-monitor/server/internal/ws"
)

func main() {
	settings := core.Load()

	// Cancelled on SIGINT/SIGTERM, which stops the sampler.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// One hub and one sampler for the process — they watch this machine,
	// so they run regardless of how many clients are attached.
	hub := ws.NewHub()
	go hub.Run()

	// Capture runs in its own goroutine and buffers; the sampler drains it.
	// A failure here is not fatal — capture is the only feature needing
	// elevated privileges, and the connections API works without it. The
	// packets stream reports its own outage over the socket.
	if err := network.StartCapture("en0"); err != nil {
		log.Printf("network: %v", err)
	}

	// Two streams, because they are different shapes of data.
	//
	// "packets" is a delta — the flows seen since the last tick. It must
	// keep draining even if nothing consumes it, or the capture accumulator
	// fills to its cap and starts discarding.
	//
	// "snapshot" is current state — what is true right now. Its parts all
	// update together, so they belong in one frame rather than racing each
	// other into the UI as separate streams.
	packets := &ws.Sampler{
		Hub:      hub,
		Interval: settings.SampleInterval,
		Type:     "packets",
		Source: func() (any, error) {
			return network.GetLivePackets()
		},
	}
	go packets.Run(ctx)

	snapshot := &ws.Sampler{
		Hub:      hub,
		Interval: settings.SampleInterval,
		Type:     "snapshot",
		Source: func() (any, error) {
			conns, err := network.CollectConnections()
			if err != nil {
				return nil, err
			}
			tp, err := network.CollectThroughput()
			if err != nil {
				return nil, err
			}
			apps, err := network.CollectApps()
			if err != nil {
				return nil, err
			}
			hosts, err := network.CollectHostnames()
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"connections": conns,
				"throughput":  tp,
				"apps":        apps,
				"hostnames":   hosts,
			}, nil
		},
	}
	go snapshot.Run(ctx)

	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     settings.CORSOrigins,
		AllowMethods:     []string{"*"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: true,
	}))

	// Liveness for this process specifically. clients comes from in-process
	// hub state, so no other service can produce this number — the gateway
	// proxies to it rather than reimplementing it.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "clients": hub.ClientCount()})
	})

	api := r.Group(settings.APIPrefix)
	routes.RegisterNetworkRoutes(api)
	routes.RegisterWSRoutes(api, hub, settings.CORSOrigins)

	addr := "127.0.0.1:" + settings.Port
	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		log.Printf("%s listening on http://%s (ws at %s/live-packets)", settings.AppName, addr, settings.APIPrefix)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
