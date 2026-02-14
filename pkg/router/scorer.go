package router

import (
	pb "github.com/kunal/gpu-batch-router/gen/inference/v1"
)

// Score calculates a routing score for a worker based on its current metrics.
// Higher score = better candidate.
//
// Formula:
//   - (vram_free / vram_total) * 100     → more free memory = better
//   - (queue_depth / 10)                  → longer queue = worse
//   - (avg_latency_ms / 10)               → higher latency = worse
//   - (gpu_utilization / 100) * 50        → busier GPU = worse
//   - 50 if temperature > 80°C           → thermal throttling penalty
func Score(m *pb.WorkerMetrics) float64 {
	if m == nil || !m.Healthy {
		return -1000
	}

	score := 0.0

	// Memory headroom (0-100 points)
	if m.VramTotalGb > 0 {
		score += (m.VramFreeGb / m.VramTotalGb) * 100
	}

	// Queue depth penalty
	score -= float64(m.QueueDepth) / 10

	// Latency penalty
	score -= m.AvgLatencyMs / 10

	// GPU utilization penalty (0-50 points)
	score -= (m.GpuUtilization / 100) * 50

	// Thermal throttling penalty
	if m.TemperatureC > 80 {
		score -= 50
	}

	return score
}
