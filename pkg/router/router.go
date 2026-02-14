package router

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
	"github.com/kunal/gpu-batch-router/pkg/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

//go:embed dashboard/*
var dashboardFS embed.FS

// Router is the main routing service.
type Router struct {
	pb.UnimplementedInferenceServiceServer

	cfg         *config.Config
	registry    *Registry
	poller      *Poller
	broadcaster *Broadcaster

	// Routing stats
	mu                  sync.RWMutex
	routingDistribution map[string]*atomic.Int64
	totalRequests       atomic.Int64
}

// New creates a new Router.
func New(cfg *config.Config) (*Router, error) {
	if len(cfg.WorkerEndpoints) == 0 {
		return nil, fmt.Errorf("no worker endpoints configured (set WORKER_ENDPOINTS)")
	}

	registry := NewRegistry(cfg.WorkerEndpoints)
	broadcaster := NewBroadcaster()

	r := &Router{
		cfg:                 cfg,
		registry:            registry,
		broadcaster:         broadcaster,
		routingDistribution: make(map[string]*atomic.Int64),
	}

	// Initialize routing distribution counters
	for _, addr := range cfg.WorkerEndpoints {
		r.routingDistribution[addr] = &atomic.Int64{}
	}

	// Connect to all workers
	if err := registry.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to workers: %w", err)
	}

	r.poller = NewPoller(registry, cfg.PollInterval)

	return r, nil
}

// RegisterGRPC registers the router's gRPC service.
func (r *Router) RegisterGRPC(s *grpc.Server) {
	pb.RegisterInferenceServiceServer(s, r)
}

// RegisterHTTP registers the dashboard and WebSocket endpoints.
func (r *Router) RegisterHTTP(mux *http.ServeMux) {
	// WebSocket endpoint
	mux.HandleFunc("/ws", r.broadcaster.HandleWS)

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Serve embedded dashboard files
	dashContent, err := fs.Sub(dashboardFS, "dashboard")
	if err != nil {
		log.Printf("⚠️  Dashboard files not found, skipping")
		return
	}
	mux.Handle("/", http.FileServer(http.FS(dashContent)))
}

// StartPoller starts the metrics polling loop and broadcast loop.
func (r *Router) StartPoller() {
	r.poller.Start()

	// Start broadcast loop (push state to dashboard every 500ms)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			r.broadcastState()
		}
	}()
}

// Stop shuts down the router.
func (r *Router) Stop() {
	r.poller.Stop()
	r.registry.Close()
}

// Infer routes an inference request to the best available worker.
func (r *Router) Infer(ctx context.Context, req *pb.InferRequest) (*pb.InferResponse, error) {
	r.totalRequests.Add(1)

	// Try up to 3 times (original + 2 retries)
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		worker := r.pickBestWorker()
		if worker == nil {
			return nil, status.Error(codes.Unavailable, "no healthy workers available")
		}

		// Forward request to chosen worker
		resp, err := worker.InferClient.Infer(ctx, req)
		if err == nil {
			// Success — track routing distribution
			if counter, ok := r.routingDistribution[worker.Address]; ok {
				counter.Add(1)
			}
			return resp, nil
		}

		// Failure — mark worker and retry
		log.Printf("⚠️  Worker %s failed (attempt %d): %v", worker.Address, attempt+1, err)
		r.registry.MarkFailed(worker.Address)
		lastErr = err
	}

	return nil, status.Errorf(codes.Unavailable, "all workers failed: %v", lastErr)
}

// pickBestWorker selects the best worker using weighted random among top-3.
func (r *Router) pickBestWorker() *WorkerEntry {
	healthy := r.registry.GetHealthy()
	if len(healthy) == 0 {
		return nil
	}

	// Score all workers
	type scored struct {
		worker *WorkerEntry
		score  float64
	}
	candidates := make([]scored, len(healthy))
	for i, w := range healthy {
		candidates[i] = scored{worker: w, score: Score(w.Metrics)}
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Take top-3 (or fewer if less available)
	topN := 3
	if topN > len(candidates) {
		topN = len(candidates)
	}
	top := candidates[:topN]

	// Weighted random selection among top-N
	// Shift scores to be positive (min score becomes 1)
	minScore := top[topN-1].score
	totalWeight := 0.0
	weights := make([]float64, topN)
	for i, c := range top {
		weights[i] = c.score - minScore + 1 // +1 to avoid zero weight
		totalWeight += weights[i]
	}

	// Weighted random pick
	r_ := rand.Float64() * totalWeight
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r_ <= cumulative {
			return top[i].worker
		}
	}

	// Fallback: return the best
	return top[0].worker
}

// broadcastState pushes cluster state to dashboard clients.
func (r *Router) broadcastState() {
	workers := r.registry.GetAll()
	state := &ClusterState{
		Workers:             make([]WorkerState, 0, len(workers)),
		RoutingDistribution: make(map[string]int64),
		TotalRequests:       r.totalRequests.Load(),
	}

	for _, w := range workers {
		ws := WorkerState{
			Address: w.Address,
			Healthy: w.Healthy,
		}
		if w.Metrics != nil {
			ws.ID = w.Metrics.WorkerId
			ws.Score = Score(w.Metrics)
			ws.VRAMFreeGB = w.Metrics.VramFreeGb
			ws.VRAMTotalGB = w.Metrics.VramTotalGb
			ws.GPUUtilization = w.Metrics.GpuUtilization
			ws.TemperatureC = w.Metrics.TemperatureC
			ws.QueueDepth = w.Metrics.QueueDepth
			ws.AvgLatencyMs = w.Metrics.AvgLatencyMs
			ws.CurrentBatch = w.Metrics.CurrentBatch
		}
		state.Workers = append(state.Workers, ws)
	}

	for addr, counter := range r.routingDistribution {
		state.RoutingDistribution[addr] = counter.Load()
	}

	r.broadcaster.Broadcast(state)
}
