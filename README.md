# GPU-Aware Intelligent Batch Router 🚀

A production-grade GPU inference routing system in Go that intelligently routes requests to the best GPU worker using real-time metrics, adaptive micro-batching, and priority queues.

**Demo target:** Google Colab (free T4 GPU) → K3s → NVIDIA GPU Time-Slicing (1 GPU → 3 virtual GPUs)

![Control Center](pkg/router/dashboard/screenshot.png)

---

## Architecture

```
Client ──gRPC──▶ Router ──score & route──▶ Worker 1 (vGPU-0)
                   │                      Worker 2 (vGPU-1)  
                   │                      Worker 3 (vGPU-2)
                   │
                   ├── Scores workers by: VRAM, queue depth, latency, GPU util, temperature
                   ├── Anti-thundering-herd: weighted random among top-3
                   ├── Retry + failover: 2 retries, marks unhealthy after 3 failures
                   └── Dashboard: real-time WebSocket updates at :8080
```

Each **Worker** implements:
- **Priority Queue** — HIGH requests skip ahead of LOW (QoS)
- **Adaptive Micro-Batching** — collects 1-32 requests per batch, adapts wait time based on queue pressure
- **GPU Executor** — real ONNX Runtime inference (ResNet-50) or simulation fallback
- **NVML Metrics** — real GPU temp/VRAM/utilization via CGo bindings

---

## Quick Start (Local — Simulated GPU)

```bash
# Start 3 workers
WORKER_ID=worker-1 WORKER_PORT=50052 METRICS_PORT=9091 go run ./cmd/worker/ &
WORKER_ID=worker-2 WORKER_PORT=50053 METRICS_PORT=9092 go run ./cmd/worker/ &
WORKER_ID=worker-3 WORKER_PORT=50054 METRICS_PORT=9093 go run ./cmd/worker/ &

# Start router
WORKER_ENDPOINTS=localhost:50052,localhost:50053,localhost:50054 go run ./cmd/router/

# Open dashboard
open http://localhost:8080

# Run load test (in another terminal)
go run scripts/loadtest.go --addr=localhost:50051 --concurrency=50 --duration=30s
```

## Deploy on Google Colab (Real T4 GPU)

1. Open a Colab notebook with **T4 GPU** runtime
2. Clone this repo:
   ```python
   !git clone https://github.com/kunal/gpu-batch-router.git
   %cd gpu-batch-router
   ```
3. Run the one-click setup:
   ```python
   !bash scripts/setup_colab.sh
   ```

This installs K3s, enables GPU time-slicing (1 T4 → 3 vGPUs), builds with real ONNX + NVML, deploys to K3s, and tunnels the dashboard via ngrok.

## Docker Compose (Local)

```bash
docker compose -f deploy/docker-compose.yaml up --build
# Dashboard at http://localhost:8080
```

---

## Project Structure

```
├── proto/inference/v1/inference.proto   # gRPC service definitions
├── gen/inference/v1/                    # Generated Go code
├── cmd/
│   ├── router/main.go                  # Router entrypoint
│   └── worker/main.go                  # Worker entrypoint
├── pkg/
│   ├── router/
│   │   ├── router.go                   # Core routing + retry + anti-thundering-herd
│   │   ├── scorer.go                   # GPU scoring algorithm
│   │   ├── registry.go                 # Worker health tracking
│   │   ├── poller.go                   # Metrics polling
│   │   ├── broadcast.go                # WebSocket for dashboard
│   │   └── dashboard/index.html        # Real-time control center
│   ├── worker/
│   │   ├── server.go                   # gRPC worker server
│   │   ├── queue.go                    # Heap-based priority queue
│   │   ├── batcher.go                  # Adaptive micro-batching engine
│   │   ├── metrics.go                  # GPU metrics (simulated + real NVML)
│   │   ├── executor/                   # GPU executor (simulation + ONNX)
│   │   └── nvml/                       # NVIDIA GPU bindings (CGo, dlopen)
│   └── config/config.go                # Environment-based config
├── deploy/
│   ├── docker-compose.yaml             # Local dev (3 workers + router)
│   ├── docker/Dockerfile.*             # Multi-stage Docker builds
│   └── k8s/                            # K3s manifests + GPU time-slicing
├── scripts/
│   ├── setup_colab.sh                  # One-click Colab deployment
│   ├── loadtest.go                     # gRPC load test client
│   └── gen-proto.sh                    # Proto code generation
└── Makefile
```

## Configuration (Environment Variables)

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_ID` | `worker-0` | Unique worker identifier |
| `ROUTER_PORT` | `50051` | Router gRPC port |
| `WORKER_PORT` | `50052` | Worker gRPC port |
| `DASHBOARD_PORT` | `8080` | Dashboard HTTP port |
| `METRICS_PORT` | `9090` | Prometheus metrics port |
| `MAX_BATCH_SIZE` | `32` | Maximum batch size |
| `MAX_WAIT_MS` | `50` | Max time to wait for batch to fill (ms) |
| `POLL_INTERVAL_MS` | `500` | How often router polls worker metrics |
| `WORKER_ENDPOINTS` | — | Comma-separated worker addresses |
| `EXECUTOR_TYPE` | `simulation` | `simulation` or `onnx` |
| `USE_NVML` | `auto` | `auto`, `true`, or `false` |
| `ONNX_MODEL_PATH` | `/models/resnet50.onnx` | Path to ONNX model file |

## Build Tags

| Tag | Effect |
|-----|--------|
| (none) | Simulation mode — works everywhere |
| `-tags onnx` | Real ONNX Runtime inference (requires libonnxruntime) |
| `-tags nvml` | Real NVIDIA GPU metrics (requires libnvidia-ml.so) |
| `-tags "onnx,nvml"` | Full GPU mode (Colab) |

---

## What's Real vs Simulated

| Component | Local (default) | Colab (`-tags "onnx,nvml"`) |
|-----------|----------------|----------------------------|
| gRPC routing | ✅ Real | ✅ Real |
| Priority queue | ✅ Real | ✅ Real |
| Adaptive batching | ✅ Real | ✅ Real |
| Scoring algorithm | ✅ Real | ✅ Real |
| Retry/failover | ✅ Real | ✅ Real |
| Dashboard (WebSocket) | ✅ Real | ✅ Real |
| GPU metrics | 🔶 Simulated (reactive) | ✅ Real NVML |
| AI inference | 🔶 Simulated (sleep + matrix) | ✅ Real ONNX (ResNet-50) |
| GPU hardware | ❌ CPU only | ✅ Tesla T4 (15GB) |

---

## License

MIT
