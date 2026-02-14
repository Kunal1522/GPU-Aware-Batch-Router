//go:build !onnx

package worker

import (
	"github.com/kunal/gpu-batch-router/pkg/config"
	"github.com/kunal/gpu-batch-router/pkg/worker/executor"
)

// createExecutor returns the simulation executor (default build).
// For real ONNX inference, build with: go build -tags onnx
func createExecutor(cfg *config.Config) executor.GPUExecutor {
	return executor.NewSimulated(5)
}
