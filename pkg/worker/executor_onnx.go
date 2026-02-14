//go:build onnx

package worker

import (
	"log"
	"os"

	"github.com/kunal/gpu-batch-router/pkg/config"
	"github.com/kunal/gpu-batch-router/pkg/worker/executor"
)

// createExecutor returns the ONNX executor (GPU build).
// Build with: go build -tags onnx
func createExecutor(cfg *config.Config) executor.GPUExecutor {
	modelPath := os.Getenv("ONNX_MODEL_PATH")
	if modelPath == "" {
		modelPath = "/models/resnet50.onnx"
	}
	useGPU := cfg.UseNVML != "false"
	onnxExec, err := executor.NewONNX(modelPath, useGPU)
	if err != nil {
		log.Printf("⚠️  ONNX init failed: %v — falling back to simulation", err)
		return executor.NewSimulated(5)
	}
	log.Printf("🧠 ONNX executor loaded: model=%s, gpu=%v", modelPath, useGPU)
	return onnxExec
}
