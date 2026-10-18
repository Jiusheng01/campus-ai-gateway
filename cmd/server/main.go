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
	if cfg.AdminAuthModeTrustedHeader() && len(cfg.AdminProxyCIDRs) == 0 {
		// Not fatal, because the mode predates the list and existing
		// deployments must keep starting. Silence would be worse: an operator
		// reading the logs should be able to tell that the only condition on
		// administrator access is a string they share with a proxy.
		log.Printf("WARNING: ADMIN_AUTH_MODE=trusted-header without ADMIN_PROXY_CIDRS; any peer that knows ADMIN_PROXY_SECRET can act as super_admin")
	}
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
