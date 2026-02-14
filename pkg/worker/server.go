package worker

import (
	"context"
	"log"
	"net/http"
	"time"

	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
	"github.com/kunal/gpu-batch-router/pkg/config"
	"github.com/kunal/gpu-batch-router/pkg/worker/executor"
	"google.golang.org/grpc"
)

// Worker is the main worker service.
type Worker struct {
	pb.UnimplementedInferenceServiceServer
	pb.UnimplementedWorkerMetricsServiceServer

	cfg     *config.Config
	queue   *PriorityQueue
	batcher *Batcher
	metrics *MetricsCollector
	exec    executor.GPUExecutor
}

// New creates a new Worker with the given configuration.
func New(cfg *config.Config) (*Worker, error) {
	queue := NewPriorityQueue()

	// Create executor — defaults to simulation.
	// Build with `go build -tags onnx` for real ONNX inference.
	exec := createExecutor(cfg)
	log.Printf("🔧 Executor: %s", exec.Name())

	batcher := NewBatcher(BatcherConfig{
		MaxBatchSize: cfg.MaxBatchSize,
		MaxWaitTime:  cfg.MaxWaitTime,
		MinBatchSize: 1,
	}, queue, exec)

	metrics := NewMetricsCollector(cfg.WorkerID, batcher, queue, cfg.UseNVML)

	return &Worker{
		cfg:     cfg,
		queue:   queue,
		batcher: batcher,
		metrics: metrics,
		exec:    exec,
	}, nil
}

// RegisterGRPC registers the worker's gRPC services.
func (w *Worker) RegisterGRPC(s *grpc.Server) {
	pb.RegisterInferenceServiceServer(s, w)
	pb.RegisterWorkerMetricsServiceServer(s, w)
}

// RegisterMetricsHTTP registers the /metrics HTTP endpoint.
func (w *Worker) RegisterMetricsHTTP(mux *http.ServeMux) {
	mux.HandleFunc("/metrics", w.metrics.ServePrometheus)
	mux.HandleFunc("/health", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte("OK"))
	})
}

// StartBatcher starts the micro-batching engine.
func (w *Worker) StartBatcher() {
	w.batcher.Start()
}

// Stop shuts down the worker gracefully.
func (w *Worker) Stop() {
	w.batcher.Stop()
}

// Infer handles a single inference request via gRPC.
// It enqueues the request into the priority queue and blocks
// until the batcher processes it and returns a result.
func (w *Worker) Infer(ctx context.Context, req *pb.InferRequest) (*pb.InferResponse, error) {
	w.metrics.IncrInFlight()
	defer w.metrics.DecrInFlight()

	pending := &PendingRequest{
		Req:       req,
		DoneCh:    make(chan *pb.InferResponse, 1),
		ErrCh:     make(chan error, 1),
		EnqueueAt: time.Now(),
	}

	// Enqueue into priority queue
	w.queue.Enqueue(pending)
	// Signal batcher that new work is available
	w.batcher.Signal()

	// Block until result is ready or context cancelled
	select {
	case resp := <-pending.DoneCh:
		resp.WorkerId = w.cfg.WorkerID
		return resp, nil
	case err := <-pending.ErrCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetMetrics returns current GPU + worker metrics.
func (w *Worker) GetMetrics(ctx context.Context, req *pb.MetricsRequest) (*pb.WorkerMetrics, error) {
	return w.metrics.GetMetrics(), nil
}
