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
	"github.com/kunal/gpu-batch-router/pkg/worker"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.Load()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("⚡ Worker %s starting on port %d", cfg.WorkerID, cfg.WorkerPort)
	log.Printf("   Metrics on port %d", cfg.MetricsPort)
	log.Printf("   Executor: %s | NVML: %s", cfg.ExecutorType, cfg.UseNVML)
	log.Printf("   Batch: max_size=%d, max_wait=%v", cfg.MaxBatchSize, cfg.MaxWaitTime)

	// Create the worker
	w, err := worker.New(cfg)
	if err != nil {
		log.Fatalf("❌ Failed to create worker: %v", err)
	}

	// Start the batcher
	w.StartBatcher()

	// Start gRPC server
	grpcServer := grpc.NewServer()
	w.RegisterGRPC(grpcServer)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.WorkerPort))
	if err != nil {
		log.Fatalf("❌ Failed to listen on port %d: %v", cfg.WorkerPort, err)
	}

	// Start metrics HTTP server
	go func() {
		mux := http.NewServeMux()
		w.RegisterMetricsHTTP(mux)
		addr := fmt.Sprintf(":%d", cfg.MetricsPort)
		log.Printf("📊 Metrics endpoint on %s/metrics", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("❌ Metrics server failed: %v", err)
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
	log.Println("🛑 Shutting down worker...")
	grpcServer.GracefulStop()
	w.Stop()
	log.Println("✅ Worker stopped")
}
