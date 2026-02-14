//go:build nvml

package nvml

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

// NVML types
typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

typedef struct {
    unsigned long long total;
    unsigned long long free;
    unsigned long long used;
} nvmlMemory_t;

typedef struct {
    unsigned int gpu;
    unsigned int memory;
} nvmlUtilization_t;

// Function pointers
static void* nvml_lib = NULL;

typedef nvmlReturn_t (*nvmlInit_t)(void);
typedef nvmlReturn_t (*nvmlShutdown_t)(void);
typedef nvmlReturn_t (*nvmlDeviceGetCount_t)(unsigned int*);
typedef nvmlReturn_t (*nvmlDeviceGetHandleByIndex_t)(unsigned int, nvmlDevice_t*);
typedef nvmlReturn_t (*nvmlDeviceGetMemoryInfo_t)(nvmlDevice_t, nvmlMemory_t*);
typedef nvmlReturn_t (*nvmlDeviceGetUtilizationRates_t)(nvmlDevice_t, nvmlUtilization_t*);
typedef nvmlReturn_t (*nvmlDeviceGetTemperature_t)(nvmlDevice_t, int, unsigned int*);
typedef nvmlReturn_t (*nvmlDeviceGetName_t)(nvmlDevice_t, char*, unsigned int);

static nvmlInit_t f_nvmlInit = NULL;
static nvmlShutdown_t f_nvmlShutdown = NULL;
static nvmlDeviceGetCount_t f_nvmlDeviceGetCount = NULL;
static nvmlDeviceGetHandleByIndex_t f_nvmlDeviceGetHandleByIndex = NULL;
static nvmlDeviceGetMemoryInfo_t f_nvmlDeviceGetMemoryInfo = NULL;
static nvmlDeviceGetUtilizationRates_t f_nvmlDeviceGetUtilizationRates = NULL;
static nvmlDeviceGetTemperature_t f_nvmlDeviceGetTemperature = NULL;
static nvmlDeviceGetName_t f_nvmlDeviceGetName = NULL;

static int nvml_load() {
    nvml_lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY);
    if (!nvml_lib) {
        nvml_lib = dlopen("libnvidia-ml.so", RTLD_LAZY);
    }
    if (!nvml_lib) return -1;

    f_nvmlInit = (nvmlInit_t)dlsym(nvml_lib, "nvmlInit_v2");
    if (!f_nvmlInit) f_nvmlInit = (nvmlInit_t)dlsym(nvml_lib, "nvmlInit");
    f_nvmlShutdown = (nvmlShutdown_t)dlsym(nvml_lib, "nvmlShutdown");
    f_nvmlDeviceGetCount = (nvmlDeviceGetCount_t)dlsym(nvml_lib, "nvmlDeviceGetCount_v2");
    if (!f_nvmlDeviceGetCount) f_nvmlDeviceGetCount = (nvmlDeviceGetCount_t)dlsym(nvml_lib, "nvmlDeviceGetCount");
    f_nvmlDeviceGetHandleByIndex = (nvmlDeviceGetHandleByIndex_t)dlsym(nvml_lib, "nvmlDeviceGetHandleByIndex_v2");
    if (!f_nvmlDeviceGetHandleByIndex) f_nvmlDeviceGetHandleByIndex = (nvmlDeviceGetHandleByIndex_t)dlsym(nvml_lib, "nvmlDeviceGetHandleByIndex");
    f_nvmlDeviceGetMemoryInfo = (nvmlDeviceGetMemoryInfo_t)dlsym(nvml_lib, "nvmlDeviceGetMemoryInfo");
    f_nvmlDeviceGetUtilizationRates = (nvmlDeviceGetUtilizationRates_t)dlsym(nvml_lib, "nvmlDeviceGetUtilizationRates");
    f_nvmlDeviceGetTemperature = (nvmlDeviceGetTemperature_t)dlsym(nvml_lib, "nvmlDeviceGetTemperature");
    f_nvmlDeviceGetName = (nvmlDeviceGetName_t)dlsym(nvml_lib, "nvmlDeviceGetName");

    if (!f_nvmlInit || !f_nvmlDeviceGetCount || !f_nvmlDeviceGetHandleByIndex) return -2;

    return f_nvmlInit();
}

static int nvml_device_count() {
    unsigned int count = 0;
    if (f_nvmlDeviceGetCount) f_nvmlDeviceGetCount(&count);
    return (int)count;
}

static int nvml_get_memory(int idx, unsigned long long* total, unsigned long long* free, unsigned long long* used) {
    nvmlDevice_t dev;
    if (f_nvmlDeviceGetHandleByIndex(idx, &dev) != 0) return -1;
    nvmlMemory_t mem;
    if (f_nvmlDeviceGetMemoryInfo(dev, &mem) != 0) return -2;
    *total = mem.total;
    *free = mem.free;
    *used = mem.used;
    return 0;
}

static int nvml_get_utilization(int idx, unsigned int* gpu, unsigned int* mem) {
    nvmlDevice_t dev;
    if (f_nvmlDeviceGetHandleByIndex(idx, &dev) != 0) return -1;
    nvmlUtilization_t util;
    if (!f_nvmlDeviceGetUtilizationRates) return -2;
    if (f_nvmlDeviceGetUtilizationRates(dev, &util) != 0) return -3;
    *gpu = util.gpu;
    *mem = util.memory;
    return 0;
}

static int nvml_get_temperature(int idx, unsigned int* temp) {
    nvmlDevice_t dev;
    if (f_nvmlDeviceGetHandleByIndex(idx, &dev) != 0) return -1;
    if (!f_nvmlDeviceGetTemperature) return -2;
    // NVML_TEMPERATURE_GPU = 0
    if (f_nvmlDeviceGetTemperature(dev, 0, temp) != 0) return -3;
    return 0;
}

static int nvml_get_name(int idx, char* name, int len) {
    nvmlDevice_t dev;
    if (f_nvmlDeviceGetHandleByIndex(idx, &dev) != 0) return -1;
    if (!f_nvmlDeviceGetName) return -2;
    if (f_nvmlDeviceGetName(dev, name, len) != 0) return -3;
    return 0;
}

static void nvml_shutdown() {
    if (f_nvmlShutdown) f_nvmlShutdown();
    if (nvml_lib) dlclose(nvml_lib);
}
*/
import "C"

import (
	"fmt"
	"log"
)

// GPUInfo holds real GPU metrics from NVML.
type GPUInfo struct {
	Name           string
	Index          int
	MemoryTotalGB  float64
	MemoryFreeGB   float64
	MemoryUsedGB   float64
	GPUUtilization float64
	MemUtilization float64
	TemperatureC   float64
}

// NVML wraps NVIDIA Management Library via dlopen (no compile-time dependency).
type NVML struct {
	available bool
	gpuCount  int
}

// New attempts to load libnvidia-ml.so and initialize NVML.
// Returns (nil, err) if no NVIDIA GPU is available — this is NOT fatal.
func New() (*NVML, error) {
	rc := C.nvml_load()
	if rc != 0 {
		return nil, fmt.Errorf("NVML not available (code %d) — no NVIDIA GPU detected", rc)
	}

	count := int(C.nvml_device_count())
	if count == 0 {
		C.nvml_shutdown()
		return nil, fmt.Errorf("NVML loaded but no GPUs found")
	}

	log.Printf("🎮 NVML initialized: %d GPU(s) detected", count)

	// Print GPU names
	for i := 0; i < count; i++ {
		var name [256]C.char
		if C.nvml_get_name(C.int(i), &name[0], 256) == 0 {
			log.Printf("   GPU %d: %s", i, C.GoString(&name[0]))
		}
	}

	return &NVML{available: true, gpuCount: count}, nil
}

// Available returns true if NVML is loaded and GPUs are detected.
func (n *NVML) Available() bool {
	return n != nil && n.available
}

// GPUCount returns the number of GPUs.
func (n *NVML) GPUCount() int {
	if n == nil {
		return 0
	}
	return n.gpuCount
}

// GetGPUInfo returns real-time metrics for a specific GPU index.
func (n *NVML) GetGPUInfo(index int) (*GPUInfo, error) {
	if !n.Available() {
		return nil, fmt.Errorf("NVML not available")
	}
	if index >= n.gpuCount {
		return nil, fmt.Errorf("GPU index %d out of range (have %d)", index, n.gpuCount)
	}

	info := &GPUInfo{Index: index}

	// GPU name
	var name [256]C.char
	if C.nvml_get_name(C.int(index), &name[0], 256) == 0 {
		info.Name = C.GoString(&name[0])
	}

	// Memory
	var total, free, used C.ulonglong
	if C.nvml_get_memory(C.int(index), &total, &free, &used) == 0 {
		info.MemoryTotalGB = float64(total) / (1024 * 1024 * 1024)
		info.MemoryFreeGB = float64(free) / (1024 * 1024 * 1024)
		info.MemoryUsedGB = float64(used) / (1024 * 1024 * 1024)
	}

	// Utilization
	var gpuUtil, memUtil C.uint
	if C.nvml_get_utilization(C.int(index), &gpuUtil, &memUtil) == 0 {
		info.GPUUtilization = float64(gpuUtil)
		info.MemUtilization = float64(memUtil)
	}

	// Temperature
	var temp C.uint
	if C.nvml_get_temperature(C.int(index), &temp) == 0 {
		info.TemperatureC = float64(temp)
	}

	return info, nil
}

// Shutdown cleans up NVML resources.
func (n *NVML) Shutdown() {
	if n != nil && n.available {
		C.nvml_shutdown()
		n.available = false
	}
}
