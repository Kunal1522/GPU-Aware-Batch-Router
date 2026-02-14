package worker

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
)

// MetricsCollector gathers GPU metrics (real NVML or simulated).
type MetricsCollector struct {
	workerID string
	batcher  *Batcher
	queue    *PriorityQueue

	// Simulated GPU state
	mu             sync.RWMutex
	simVRAMUsedGB  float64
	simVRAMTotalGB float64
	simTempC       float64
	simGPUUtil     float64

	// Track request count for utilization simulation
	inFlight atomic.Int32

	useNVML bool
}

func NewMetricsCollector(workerID string, batcher *Batcher, queue *PriorityQueue, useNVML string) *MetricsCollector {
	mc := &MetricsCollector{
		workerID:       workerID,
		batcher:        batcher,
		queue:          queue,
		simVRAMTotalGB: 5.0, // 5GB vGPU slice (T4 / 3)
		simVRAMUsedGB:  0.8, // base ONNX model footprint
		simTempC:       42.0,
		simGPUUtil:     0.0,
	}

	// Check if NVML is available
	if useNVML == "true" || (useNVML == "auto" && mc.tryNVML()) {
		mc.useNVML = true
		log.Printf("📊 Metrics: using REAL NVML")
	} else {
		mc.useNVML = false
		log.Printf("📊 Metrics: using SIMULATED GPU stats")
	}

	// Start background simulation ticker
	if !mc.useNVML {
		go mc.simulationLoop()
	}

	return mc
}

// GetMetrics returns current worker metrics as a protobuf message.
func (mc *MetricsCollector) GetMetrics() *pb.WorkerMetrics {
	if mc.useNVML {
		return mc.getRealMetrics()
	}
	return mc.getSimulatedMetrics()
}

func (mc *MetricsCollector) getSimulatedMetrics() *pb.WorkerMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	return &pb.WorkerMetrics{
		WorkerId:       mc.workerID,
		VramFreeGb:     mc.simVRAMTotalGB - mc.simVRAMUsedGB,
		VramTotalGb:    mc.simVRAMTotalGB,
		QueueDepth:     int32(mc.queue.Depth()),
		AvgLatencyMs:   float64(mc.batcher.AvgLatencyMs.Load()),
		GpuUtilization: mc.simGPUUtil,
		TemperatureC:   mc.simTempC,
		CurrentBatch:   mc.batcher.LastBatchSize.Load(),
		Healthy:        true,
	}
}

func (mc *MetricsCollector) getRealMetrics() *pb.WorkerMetrics {
	// TODO: Implement real NVML via CGo
	// For now, fall back to simulated
	return mc.getSimulatedMetrics()
}

func (mc *MetricsCollector) tryNVML() bool {
	// TODO: Try to dlopen libnvidia-ml.so
	// Return true if successful
	return false
}

// simulationLoop updates simulated GPU metrics based on actual worker load.
func (mc *MetricsCollector) simulationLoop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		mc.mu.Lock()

		queueDepth := float64(mc.queue.Depth())
		batchSize := float64(mc.batcher.LastBatchSize.Load())
		inFlight := float64(mc.inFlight.Load())

		// GPU utilization: based on queue depth and in-flight requests
		targetUtil := math.Min(100, (queueDepth*3)+(inFlight*15)+(batchSize*2))
		// Smooth transition (exponential decay)
		mc.simGPUUtil = mc.simGPUUtil*0.7 + targetUtil*0.3

		// VRAM: base footprint + proportional to batch activity
		mc.simVRAMUsedGB = 0.8 + (batchSize/32.0)*2.5
		mc.simVRAMUsedGB = math.Min(mc.simVRAMUsedGB, mc.simVRAMTotalGB-0.2)

		// Temperature: rises with utilization, cools at idle
		targetTemp := 42.0 + (mc.simGPUUtil/100.0)*38.0 // 42°C idle → 80°C full load
		mc.simTempC = mc.simTempC*0.9 + targetTemp*0.1
		// Add slight noise
		mc.simTempC += (rand.Float64() - 0.5) * 0.5

		mc.mu.Unlock()
	}
}

// IncrInFlight / DecrInFlight track active requests for utilization simulation.
func (mc *MetricsCollector) IncrInFlight() { mc.inFlight.Add(1) }
func (mc *MetricsCollector) DecrInFlight() { mc.inFlight.Add(-1) }

// ServePrometheus writes Prometheus-format metrics to HTTP response.
func (mc *MetricsCollector) ServePrometheus(w http.ResponseWriter, r *http.Request) {
	m := mc.GetMetrics()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP gpu_vram_free_gb Free VRAM in GB\n")
	fmt.Fprintf(w, "# TYPE gpu_vram_free_gb gauge\n")
	fmt.Fprintf(w, "gpu_vram_free_gb{worker=\"%s\"} %.2f\n", m.WorkerId, m.VramFreeGb)
	fmt.Fprintf(w, "# HELP gpu_vram_total_gb Total VRAM in GB\n")
	fmt.Fprintf(w, "# TYPE gpu_vram_total_gb gauge\n")
	fmt.Fprintf(w, "gpu_vram_total_gb{worker=\"%s\"} %.2f\n", m.WorkerId, m.VramTotalGb)
	fmt.Fprintf(w, "# HELP gpu_utilization GPU utilization percentage\n")
	fmt.Fprintf(w, "# TYPE gpu_utilization gauge\n")
	fmt.Fprintf(w, "gpu_utilization{worker=\"%s\"} %.2f\n", m.WorkerId, m.GpuUtilization)
	fmt.Fprintf(w, "# HELP gpu_temperature_celsius GPU temperature\n")
	fmt.Fprintf(w, "# TYPE gpu_temperature_celsius gauge\n")
	fmt.Fprintf(w, "gpu_temperature_celsius{worker=\"%s\"} %.1f\n", m.WorkerId, m.TemperatureC)
	fmt.Fprintf(w, "# HELP worker_queue_depth Current queue depth\n")
	fmt.Fprintf(w, "# TYPE worker_queue_depth gauge\n")
	fmt.Fprintf(w, "worker_queue_depth{worker=\"%s\"} %d\n", m.WorkerId, m.QueueDepth)
	fmt.Fprintf(w, "# HELP worker_avg_latency_ms Average batch latency\n")
	fmt.Fprintf(w, "# TYPE worker_avg_latency_ms gauge\n")
	fmt.Fprintf(w, "worker_avg_latency_ms{worker=\"%s\"} %.2f\n", m.WorkerId, m.AvgLatencyMs)
	fmt.Fprintf(w, "# HELP worker_batch_size Last batch size\n")
	fmt.Fprintf(w, "# TYPE worker_batch_size gauge\n")
	fmt.Fprintf(w, "worker_batch_size{worker=\"%s\"} %d\n", m.WorkerId, m.CurrentBatch)
	fmt.Fprintf(w, "# HELP worker_total_batches Total batches processed\n")
	fmt.Fprintf(w, "# TYPE worker_total_batches counter\n")
	fmt.Fprintf(w, "worker_total_batches{worker=\"%s\"} %d\n", m.WorkerId, mc.batcher.TotalBatches.Load())
	fmt.Fprintf(w, "# HELP worker_total_requests Total requests processed\n")
	fmt.Fprintf(w, "# TYPE worker_total_requests counter\n")
	fmt.Fprintf(w, "worker_total_requests{worker=\"%s\"} %d\n", m.WorkerId, mc.batcher.TotalRequests.Load())
}
