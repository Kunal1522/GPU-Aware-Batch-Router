package executor

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// SimulatedGPU mimics GPU computation with CPU work + sleep.
// Produces realistic latency patterns that scale with batch size.
type SimulatedGPU struct {
	BaseLatencyMs int // per-batch base latency (default 5)
}

func NewSimulated(baseLatencyMs int) *SimulatedGPU {
	if baseLatencyMs <= 0 {
		baseLatencyMs = 5
	}
	return &SimulatedGPU{BaseLatencyMs: baseLatencyMs}
}

func (s *SimulatedGPU) Name() string { return "simulation" }

func (s *SimulatedGPU) ExecuteBatch(payloads [][]byte) ([][]byte, error) {
	batchSize := len(payloads)
	if batchSize == 0 {
		return nil, fmt.Errorf("empty batch")
	}

	// Simulate GPU kernel time: base + sublinear scaling with batch size
	// Real GPUs show sublinear latency growth — batching is efficient
	latency := time.Duration(s.BaseLatencyMs) * time.Millisecond
	latency += time.Duration(float64(batchSize)*1.5) * time.Millisecond

	// Do some real CPU work (matrix multiply) to create actual load
	matrixWork(64) // 64x64 matrix multiply

	// Sleep for remaining simulated GPU time
	time.Sleep(latency)

	// Produce results
	results := make([][]byte, batchSize)
	classes := []string{"cat", "dog", "car", "tree", "person", "building", "bird", "fish"}
	for i := range results {
		result := map[string]interface{}{
			"class":      classes[rand.Intn(len(classes))],
			"confidence": 0.7 + rand.Float64()*0.29,
			"simulated":  true,
			"batch_pos":  i,
		}
		data, _ := json.Marshal(result)
		results[i] = data
	}
	return results, nil
}

// matrixWork performs an NxN matrix multiplication to create real CPU load.
func matrixWork(n int) {
	a := make([][]float64, n)
	b := make([][]float64, n)
	c := make([][]float64, n)
	for i := 0; i < n; i++ {
		a[i] = make([]float64, n)
		b[i] = make([]float64, n)
		c[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			a[i][j] = rand.Float64()
			b[i][j] = rand.Float64()
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			sum := 0.0
			for k := 0; k < n; k++ {
				sum += a[i][k] * b[k][j]
			}
			c[i][j] = sum
		}
	}
	// Prevent compiler from optimizing away the computation
	_ = math.Sqrt(c[0][0])
}
