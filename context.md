# GPU-Aware Intelligent Batch Router — Project Context

## 📌 Project Overview
A high-performance batched inference system for ResNet-50 (ONNX) that routes requests based on real-time GPU metrics.
- **Router (Local):** Handles HTTP/gRPC requests, queues, metrics, and dashboard.
- **Workers (Colab):** Run actual inference on **Tesla T4 GPUs** provided by Google Colab.
- **Connectivity:** Uses **bore.pub** (free TCP tunnels) to expose Colab workers to the local router.

## 🏗️ Architecture: Split Design
We moved to a **Split Architecture** to get the best of both worlds:
1. **Google Colab:** provides the **Real T4 GPU** (free).
2. **Your Laptop:** runs the **Router & Dashboard** (stable, no ngrok timeout issues).
3. **Bore Tunnels:** Bridge the two. Workers on Colab -> Internet -> Router on Laptop.

## ✅ Current Status (As of Last Session)
- **Codebase:** Fully up-to-date with real ONNX Runtime (CGo) and NVML bindings.
- **Timeouts:** Fixed 3 critical timeout issues to handle bore tunnel latency:
    - Poller timeout increased: 200ms → 5s
    - Router forwarding timeout: Client context → New 10s context
    - Load test timeout: Shared context → Per-request 10s timeout
- **CUDA Compatibility:** Fixed CPU fallback issue. Colab now runs CUDA 13, but the old C library was v1.17 (CUDA 11). We updated `colab_workers.sh` to use the pip-installed ONNX Runtime (v1.23+) which supports CUDA 13.

## 🚀 How to Resume Work (Next Session)

### Step 1: Start Workers on Colab
1. Open a **T4 GPU** runtime on Google Colab.
2. Clone/Pull the repo.
3. Run **ONLY** the workers script:
   ```bash
   !bash scripts/colab_workers.sh
   ```
   *(Do NOT run `setup_colab.sh` — that is for the old single-node setup)*

4. Copy the **bore addresses** printed at the end (e.g., `bore.pub:12345`).

### Step 2: Start Router on Laptop
1. go to the project folder:
   ```bash
   cd ~/Desktop/demo_yc_!
   git pull
   ```
2. Run the router with the bore addresses from Colab:
   ```bash
   WORKER_ENDPOINTS=bore.pub:PORT1,bore.pub:PORT2,bore.pub:PORT3 go run ./cmd/router/
   ```

### Step 3: View & Test
1. **Dashboard:** Open [http://localhost:8080](http://localhost:8080) on your laptop.
2. **Load Test:**
   ```bash
   go run scripts/loadtest.go --addr=localhost:50051 --concurrency=10 --duration=30s
   ```

## 🐛 Known Issues & fixes
- **"Unhealthy" Workers:** Likely due to tunnel latency. If this happens, just restart the router or check if Colab runtime disconnected.
- **GPU Usage 0%:** If this happens, verify `scripts/colab_workers.sh` was used (it has the fix). Run `!nvidia-smi` on Colab while load testing to confirm usage.

## 📂 Key Files
- `scripts/colab_workers.sh`: The master script for Colab.
- `pkg/router/router.go`: Routing logic (weighted random).
- `pkg/worker/executor/onnx.go`: Real CGo bindings for ONNX Runtime.
