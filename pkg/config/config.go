package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for both router and worker services.
type Config struct {
	// Common
	WorkerID string

	// Router
	RouterPort      int
	WorkerEndpoints []string
	PollInterval    time.Duration
	DashboardPort   int

	// Worker
	WorkerPort   int
	MetricsPort  int
	MaxBatchSize int
	MaxWaitTime  time.Duration
	ExecutorType string // "simulation" or "onnx"
	UseNVML      string // "auto", "true", "false"
}

// Load reads configuration from environment variables with sane defaults.
func Load() *Config {
	c := &Config{
		WorkerID:     envStr("WORKER_ID", "worker-0"),
		RouterPort:   envInt("ROUTER_PORT", 50051),
		WorkerPort:   envInt("WORKER_PORT", 50052),
		MetricsPort:  envInt("METRICS_PORT", 9090),
		DashboardPort: envInt("DASHBOARD_PORT", 8080),
		MaxBatchSize: envInt("MAX_BATCH_SIZE", 32),
		MaxWaitTime:  time.Duration(envInt("MAX_WAIT_MS", 50)) * time.Millisecond,
		PollInterval: time.Duration(envInt("POLL_INTERVAL_MS", 500)) * time.Millisecond,
		ExecutorType: envStr("EXECUTOR_TYPE", "simulation"),
		UseNVML:      envStr("USE_NVML", "auto"),
	}

	// Parse worker endpoints: "host1:port1,host2:port2,..."
	if eps := os.Getenv("WORKER_ENDPOINTS"); eps != "" {
		c.WorkerEndpoints = strings.Split(eps, ",")
	}

	return c
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
