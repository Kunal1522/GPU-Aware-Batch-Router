package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kunal/gpu-batch-router/pkg/config"
	"github.com/kunal/gpu-batch-router/pkg/router"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.Load()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("🧠 Router starting on port %d", cfg.RouterPort)
	log.Printf("   Dashboard on port %d", cfg.DashboardPort)
	log.Printf("   Workers: %v", cfg.WorkerEndpoints)

	// Create the router
	r, err := router.New(cfg)
	if err != nil {
		log.Fatalf("❌ Failed to create router: %v", err)
	}

	// Start metrics poller
	r.StartPoller()

	// Start gRPC server
	grpcServer := grpc.NewServer()
	r.RegisterGRPC(grpcServer)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.RouterPort))
	if err != nil {
		log.Fatalf("❌ Failed to listen on port %d: %v", cfg.RouterPort, err)
	}

	// Start dashboard HTTP + WebSocket server
	go func() {
		mux := http.NewServeMux()
		r.RegisterHTTP(mux)
		addr := fmt.Sprintf(":%d", cfg.DashboardPort)
		log.Printf("📊 Dashboard listening on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("❌ Dashboard server failed: %v", err)
		}
	}()

	// Start gRPC in background
	go func() {
		log.Printf("🚀 gRPC server listening on %s", lis.Addr().String())
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("❌ gRPC server failed: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("🛑 Shutting down router...")
	grpcServer.GracefulStop()
	r.Stop()
	log.Println("✅ Router stopped")
}
