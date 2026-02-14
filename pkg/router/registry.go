package router

import (
	"log"
	"sync"

	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// WorkerEntry tracks a single worker's state.
type WorkerEntry struct {
	Address       string
	Conn          *grpc.ClientConn
	InferClient   pb.InferenceServiceClient
	MetricsClient pb.WorkerMetricsServiceClient
	Metrics       *pb.WorkerMetrics
	FailCount     int
	Healthy       bool
}

// Registry manages the set of known workers.
type Registry struct {
	mu      sync.RWMutex
	workers map[string]*WorkerEntry // key: address
}

func NewRegistry(addrs []string) *Registry {
	r := &Registry{
		workers: make(map[string]*WorkerEntry, len(addrs)),
	}
	for _, addr := range addrs {
		r.workers[addr] = &WorkerEntry{
			Address: addr,
			Healthy: true,
			Metrics: &pb.WorkerMetrics{
				Healthy:     true,
				VramFreeGb:  5.0,
				VramTotalGb: 5.0,
			},
		}
	}
	return r
}

// Connect establishes gRPC connections to all workers.
func (r *Registry) Connect() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for addr, entry := range r.workers {
		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			log.Printf("⚠️  Failed to connect to worker %s: %v", addr, err)
			entry.Healthy = false
			continue
		}
		entry.Conn = conn
		entry.InferClient = pb.NewInferenceServiceClient(conn)
		entry.MetricsClient = pb.NewWorkerMetricsServiceClient(conn)
		log.Printf("✅ Connected to worker %s", addr)
	}
	return nil
}

// GetHealthy returns all healthy worker entries.
func (r *Registry) GetHealthy() []*WorkerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*WorkerEntry, 0)
	for _, w := range r.workers {
		if w.Healthy && w.InferClient != nil {
			result = append(result, w)
		}
	}
	return result
}

// GetAll returns all worker entries.
func (r *Registry) GetAll() []*WorkerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*WorkerEntry, 0, len(r.workers))
	for _, w := range r.workers {
		result = append(result, w)
	}
	return result
}

// UpdateMetrics updates the cached metrics for a worker.
func (r *Registry) UpdateMetrics(addr string, m *pb.WorkerMetrics) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[addr]; ok {
		w.Metrics = m
		w.FailCount = 0
		w.Healthy = m.Healthy
	}
}

// MarkFailed increments the fail count for a worker.
// After 3 consecutive failures, the worker is marked unhealthy.
func (r *Registry) MarkFailed(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[addr]; ok {
		w.FailCount++
		if w.FailCount >= 3 {
			w.Healthy = false
			log.Printf("❌ Worker %s marked UNHEALTHY (3 consecutive failures)", addr)
		}
	}
}

// MarkHealthy resets a worker to healthy state.
func (r *Registry) MarkHealthy(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[addr]; ok {
		w.FailCount = 0
		w.Healthy = true
	}
}

// Close shuts down all gRPC connections.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.workers {
		if w.Conn != nil {
			w.Conn.Close()
		}
	}
}
