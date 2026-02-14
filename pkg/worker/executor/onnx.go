//go:build onnx

package executor

/*
#cgo LDFLAGS: -lonnxruntime
#include <onnxruntime_c_api.h>
#include <stdlib.h>

// Helper to create ORT environment, session, and run inference
// We use the C API directly for maximum control and portability

static const OrtApi* g_ort = NULL;
static OrtEnv* g_env = NULL;
static OrtSession* g_session = NULL;
static OrtSessionOptions* g_session_opts = NULL;
static OrtMemoryInfo* g_memory_info = NULL;
static OrtAllocator* g_allocator = NULL;

static int ort_init(const char* model_path, int use_gpu) {
    g_ort = OrtGetApiBase()->GetApi(ORT_API_VERSION);
    if (!g_ort) return -1;

    OrtStatus* status = NULL;

    // Create environment
    status = g_ort->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "gpu-batch-router", &g_env);
    if (status) { g_ort->ReleaseStatus(status); return -2; }

    // Create session options
    status = g_ort->CreateSessionOptions(&g_session_opts);
    if (status) { g_ort->ReleaseStatus(status); return -3; }

    // Enable GPU if requested
    if (use_gpu) {
        status = OrtSessionOptionsAppendExecutionProvider_CUDA(g_session_opts, 0);
        if (status) {
            // CUDA not available, fall back to CPU
            g_ort->ReleaseStatus(status);
        }
    }

    // Optimize for throughput
    g_ort->SetIntraOpNumThreads(g_session_opts, 4);
    g_ort->SetSessionGraphOptimizationLevel(g_session_opts, ORT_ENABLE_ALL);

    // Create session
    status = g_ort->CreateSession(g_env, model_path, g_session_opts, &g_session);
    if (status) { g_ort->ReleaseStatus(status); return -4; }

    // Create memory info
    status = g_ort->CreateCpuMemoryInfo(OrtArenaAllocator, OrtMemTypeDefault, &g_memory_info);
    if (status) { g_ort->ReleaseStatus(status); return -5; }

    // Get allocator
    status = g_ort->GetAllocatorWithDefaultOptions(&g_allocator);
    if (status) { g_ort->ReleaseStatus(status); return -6; }

    return 0;
}

// Run inference on a batch of float data
// Input shape: [batch_size, 3, 224, 224] (ImageNet)
// Output: [batch_size, 1000] (class probabilities)
static int ort_run_batch(float* input_data, int batch_size, float* output_data) {
    if (!g_session || !g_ort) return -1;

    OrtStatus* status = NULL;
    const int64_t input_shape[] = {batch_size, 3, 224, 224};
    const size_t input_len = batch_size * 3 * 224 * 224 * sizeof(float);

    // Create input tensor
    OrtValue* input_tensor = NULL;
    status = g_ort->CreateTensorWithDataAsOrtValue(
        g_memory_info, input_data, input_len,
        input_shape, 4, ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT,
        &input_tensor
    );
    if (status) { g_ort->ReleaseStatus(status); return -2; }

    // Get input/output names
    char* input_name = NULL;
    char* output_name = NULL;
    g_ort->SessionGetInputName(g_session, 0, g_allocator, &input_name);
    g_ort->SessionGetOutputName(g_session, 0, g_allocator, &output_name);

    const char* input_names[] = { input_name };
    const char* output_names[] = { output_name };
    OrtValue* output_tensor = NULL;

    // Run inference
    status = g_ort->Run(
        g_session, NULL,
        input_names, (const OrtValue* const*)&input_tensor, 1,
        output_names, 1,
        &output_tensor
    );

    g_ort->AllocatorFree(g_allocator, input_name);
    g_ort->AllocatorFree(g_allocator, output_name);
    g_ort->ReleaseValue(input_tensor);

    if (status) {
        g_ort->ReleaseStatus(status);
        return -3;
    }

    // Copy output data
    float* out_ptr = NULL;
    g_ort->GetTensorMutableData(output_tensor, (void**)&out_ptr);
    for (int i = 0; i < batch_size * 1000; i++) {
        output_data[i] = out_ptr[i];
    }

    g_ort->ReleaseValue(output_tensor);
    return 0;
}

static void ort_cleanup() {
    if (g_session) g_ort->ReleaseSession(g_session);
    if (g_session_opts) g_ort->ReleaseSessionOptions(g_session_opts);
    if (g_memory_info) g_ort->ReleaseMemoryInfo(g_memory_info);
    if (g_env) g_ort->ReleaseEnv(g_env);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"unsafe"
)

// ImageNet class labels (top-10 common ones for display)
var imagenetLabels = []string{
	"tench", "goldfish", "great_white_shark", "tiger_shark", "hammerhead",
	"electric_ray", "stingray", "cock", "hen", "ostrich",
}

// ONNXExecutor runs real inference using ONNX Runtime.
// Supports both CPU and GPU (CUDA) execution providers.
type ONNXExecutor struct {
	mu        sync.Mutex
	modelPath string
	useGPU    bool
	ready     bool
}

// NewONNX creates an ONNX executor and loads the model.
func NewONNX(modelPath string, useGPU bool) (*ONNXExecutor, error) {
	e := &ONNXExecutor{
		modelPath: modelPath,
		useGPU:    useGPU,
	}

	cModelPath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cModelPath))

	gpuFlag := C.int(0)
	if useGPU {
		gpuFlag = 1
	}

	rc := C.ort_init(cModelPath, gpuFlag)
	if rc != 0 {
		return nil, fmt.Errorf("ONNX Runtime init failed (code %d)", rc)
	}

	e.ready = true
	return e, nil
}

func (e *ONNXExecutor) Name() string {
	if e.useGPU {
		return "onnx-gpu"
	}
	return "onnx-cpu"
}

// ExecuteBatch runs inference on a batch of payloads.
// Each payload is treated as raw bytes → float32 image data.
// If payload is too small, we pad with zeros (random noise for demo).
func (e *ONNXExecutor) ExecuteBatch(payloads [][]byte) ([][]byte, error) {
	if !e.ready {
		return nil, fmt.Errorf("ONNX executor not initialized")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	batchSize := len(payloads)
	if batchSize == 0 {
		return nil, fmt.Errorf("empty batch")
	}

	// ImageNet input: [batch, 3, 224, 224]
	inputSize := batchSize * 3 * 224 * 224
	inputData := make([]float32, inputSize)

	// Fill input data from payloads (or pad with normalized random values)
	for i, payload := range payloads {
		offset := i * 3 * 224 * 224
		for j := 0; j < 3*224*224; j++ {
			if j < len(payload)/4 {
				// Use payload bytes as float32
				inputData[offset+j] = float32(payload[j%len(payload)]) / 255.0
			} else {
				// Pad with normalized value
				inputData[offset+j] = 0.5
			}
		}
	}

	// Output: [batch, 1000]
	outputSize := batchSize * 1000
	outputData := make([]float32, outputSize)

	rc := C.ort_run_batch(
		(*C.float)(unsafe.Pointer(&inputData[0])),
		C.int(batchSize),
		(*C.float)(unsafe.Pointer(&outputData[0])),
	)
	if rc != 0 {
		return nil, fmt.Errorf("ONNX inference failed (code %d)", rc)
	}

	// Convert outputs to JSON results
	results := make([][]byte, batchSize)
	for i := 0; i < batchSize; i++ {
		offset := i * 1000
		probs := outputData[offset : offset+1000]

		// Softmax
		maxVal := float32(-math.MaxFloat32)
		for _, v := range probs {
			if v > maxVal {
				maxVal = v
			}
		}
		sum := float32(0)
		softmax := make([]float32, 1000)
		for j, v := range probs {
			softmax[j] = float32(math.Exp(float64(v - maxVal)))
			sum += softmax[j]
		}
		for j := range softmax {
			softmax[j] /= sum
		}

		// Top-5 predictions
		type pred struct {
			Class string  `json:"class"`
			Index int     `json:"index"`
			Prob  float64 `json:"probability"`
		}
		preds := make([]pred, 1000)
		for j := range preds {
			label := fmt.Sprintf("class_%d", j)
			if j < len(imagenetLabels) {
				label = imagenetLabels[j]
			}
			preds[j] = pred{Class: label, Index: j, Prob: float64(softmax[j])}
		}
		sort.Slice(preds, func(a, b int) bool { return preds[a].Prob > preds[b].Prob })

		result := map[string]interface{}{
			"top5":      preds[:5],
			"simulated": false,
			"batch_pos": i,
			"executor":  "onnx",
		}
		data, _ := json.Marshal(result)
		results[i] = data
	}

	return results, nil
}

// Cleanup releases ONNX Runtime resources.
func (e *ONNXExecutor) Cleanup() {
	C.ort_cleanup()
	e.ready = false
}
