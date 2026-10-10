package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/app"
)

func main() {
	cfg, err := app.LoadConfig()
	if err != nil { log.Fatal(err) }
	server, err := app.NewServer(cfg)
	if err != nil { log.Fatal(err) }
	defer server.Close()
	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("campus-ai-gateway listening on %s", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Fatalf("http server failed: %v", err) }
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}
