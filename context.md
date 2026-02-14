# GPU-Aware Intelligent Batch Router — The Complete Project Context

**📅 Date:** February 15, 2026
**🚀 Project:** GPU-Aware Batch Router (Go + ONNX Runtime + CUDA + K3s)
**🎯 Core Objective:** A distributed inference system that routes requests to "worker" nodes based on *real-time* GPU metrics (VRAM, Utilization, Temperature) from actual hardware (Tesla T4).

---

## 🏗️ The "Split" Architecture: Why & How
We are using a **Hybrid / Split Architecture** to leverage Google Colab's free T4 GPU while keeping the control plane (Router/Dashboard) stable on your local machine.

### 1. **Workers (The Muscle)** — Running on **Google Colab (Free Tier)**
- **Hardware:** Tesla T4 GPU (free).
- **Process Model:** 3 separate worker processes running on the *same machine*, simulating a cluster via **GPU Time-Slicing**.
- **Tech Stack:**
    - **Go 1.24:** Core worker logic (gRPC server, metrics collector).
    - **CGo Bindings:** Directly links to C/C++ libraries for performance.
    - **ONNX Runtime (C API):** Runs ResNet-50 inference.
    - **NVML (C API):** Reads real hardware metrics (Temp, Fan, VRAM).

### 2. **Router (The Brain)** — Running on **Your Laptop (Local)**
- **Role:** Central gateway. Receives HTTP/gRPC requests from clients.
- **Routing Logic:** **Weighted Random Selection** based on comprehensive worker scores.
    - `Score = (VRAM_Free * 100) - (Queue_Depth * 10) - (Latency * 10) - (GPU_Util * 50)`
- **Dashboard:** Web UI on `localhost:8080` visualizing real-time cluster state via WebSocket.

### 3. **Connectivity (The Bridge)** — via **bore.pub**
- **Problem:** Colab has no public IP.
- **Solution:** We use `bore` (a free, zero-config TCP tunnel).
    - Worker 1 (Colab:50052) → Internet → `bore.pub:XXXXX`
    - Laptop Router connects to `bore.pub:XXXXX` to reach Worker 1.

---

## 📖 The Technical Journey: Challenges & Solutions (READ THIS!)

### 🛑 Challenge 1: Networking & Connectivity
**Problem:** Colab is behind a firewall. Traditional tools like `ngrok` require accounts/tokens, rate limits, and constant restarts.
**Solution:** Switched to **`bore`**. It's open-source, requires no signup, and just works.
**Artifact:** `scripts/colab_workers.sh` automatically downloads and starts `bore` for each worker port.

### 🛑 Challenge 2: Latency & Timeouts
**Problem:** Routing over the public internet (Laptop → bore.pub → Colab) adds ~200-500ms latency per request. Our tight gRPC timeouts (200ms) caused constant failures ("Deadline Exceeded").
**Solution:** Tuned all system timeouts for wide-area network (WAN) latency:
- **Poller:** Increased metric fetch timeout from 200ms → **5s**.
- **Router Forwarding:** Middleware creates a new **10s context** for backend calls instead of inheriting short client contexts.
- **Load Test:** Client uses a **10s per-request timeout** instead of sharing one global test context.

### 🛑 Challenge 3: Real vs Fake (Simulation Trap)
**Problem:** Initially, we simulated GPU metrics. We needed *real* feedback.
**Solution:** Implemented `pkg/worker/nvml/nvml.go` using CGo to dynload `libnvidia-ml.so`. Now, "74°C" on the dashboard is the actual temperature of the T4 die in Colab.

### 💀 Challenge 4: The Silent Killer (CUDA Compatibility)
**Problem:** Colab environments recently upgraded to **CUDA 13**.
- The standard ONNX Runtime C library (v1.17) only supports CUDA 11/12.
- When we compiled against v1.17, the worker started but **silently fell back to CPU execution** (0% GPU Usage) because it couldn't load the CUDA provider.
**Solution (The "Pit Stop" Fix):**
- Updated `scripts/colab_workers.sh` to install `onnxruntime-gpu` via **pip** (which is kept up-to-date with Colab's CUDA version).
- The script dynamically finds the pip-installed shared libraries (`libonnxruntime_providers_cuda.so`).
- It links your Go binary against *those* specific libraries during the build.
- **Result:** Real GPU inference. Graph spikes. Happiness.

---

## 🛠️ Operational Guide: How to Resume (Copy-Paste)

### Step 1: Start Workers on Colab
1. Open your Google Colab notebook (Runtime → Change runtime type → **T4 GPU**).
2. Run this block to clean slate and start fresh:
   ```bash
   # Kill any zombie processes
   !pkill -f "bin/worker" 2>/dev/null; pkill -f bore 2>/dev/null
   
   # Update code & Run the Master Script
   %cd /content/GPU-Aware-Batch-Router
   !git pull
   !bash scripts/colab_workers.sh
   ```
3. **Wait** for the output. It will eventually print 3 addresses:
   ```text
   Worker-1  →  bore.pub:12345
   Worker-2  →  bore.pub:67890
   Worker-3  →  bore.pub:13579
   ```
   **COPY THESE ADDRESSES.**

### Step 2: Start Router on Laptop
1. Open terminal on your laptop:
   ```bash
   cd ~/Desktop/demo_yc_!
   git pull
   ```
2. Run the router (replace with your ACTUAL ports):
   ```bash
   WORKER_ENDPOINTS=bore.pub:12345,bore.pub:67890,bore.pub:13579 go run ./cmd/router/
   ```

### Step 3: Verify & Load Test
1. **Dashboard:** Open [http://localhost:8080](http://localhost:8080)
2. **Fire Load:** in another terminal:
   ```bash
   # Start small (conn=10) due to tunnel latency
   go run scripts/loadtest.go --addr=localhost:50051 --concurrency=10 --duration=30s
   ```
3. **Verify GPU Usage (On Colab):**
   Run `!nvidia-smi -l 1` while the test runs. You should see GPU-Util > 0% and Memory Usage increase.

---

## 🐛 Troubleshooting Handbook

| Symptom | Cause | Fix |
|---------|-------|-----|
| **"Unhealthy" Workers** | Bore tunnel connection dropped or timed out. | Restart router on laptop. If that fails, restart tunnels on Colab (re-run script). |
| **GPU Memory 0 MiB** | ONNX Runtime fell back to CPU execution. | Use `scripts/colab_workers.sh` (it has the fix). Run `!head -10 /tmp/worker-1.log` to confirm `Executor: onnx-gpu`. |
| **High Latency (>1s)** | You are running on CPU, not GPU. | See above. Also check if you're running too many concurrent requests (tunnels bottleneck >50 conn). |
| **"Deadline Exceeded"** | Tunnels are slow/network jitter. | Try reducing concurrency in load test. Increase `metrics.go` poll timeout further if needed. |

---

## ⏭️ Next Steps (Roadmap)
1. **Visual Confirmation:** Verify the GPU graph spike on Colab Resources panel during a load test.
2. **Chaos Engineering:** Kill a worker on Colab (`kill <PID>`) and watch the Laptop Dashboard mark it offline and re-route traffic.
3. **Scale Up:** Try running 2 Notebooks (6 workers total) and point your Router at all 6 for a larger cluster simulation.

**Code is live at:** `https://github.com/Kunal1522/GPU-Aware-Batch-Router`
