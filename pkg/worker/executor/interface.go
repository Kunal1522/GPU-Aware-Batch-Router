package executor

// GPUExecutor is the interface for running batched inference workloads.
// Implementations can target real GPU (ONNX) or simulation.
type GPUExecutor interface {
	// ExecuteBatch processes a batch of payloads and returns results.
	// Each payload corresponds to one inference request.
	ExecuteBatch(payloads [][]byte) ([][]byte, error)

	// Name returns the executor type for logging.
	Name() string
}
