# GPU-Aware Intelligent Batch Router — Project Context & Handover

**📅 Date:** February 15, 2026
**🚀 Project:** GPU-Aware Batch Router (Go + ONNX Runtime + CUDA + K3s)
**🎯 Goal:** Route requests to "worker" nodes based on *real-time* GPU metrics (VRAM, Util, Temp) from a Tesla T4.

---

## 🏗️ Architecture: The "Split" Design
We are using a **hybrid/split architecture** to leverage Google Colab's free GPU while keeping the control plane stable on your local machine.

| Component | Location | Responsibility | Technical Details |
|-----------|----------|----------------|-------------------|
| **Workers (x3)** | **Google Colab** | Run AI Inference (ResNet-50) & Collections GPU Metrics | Runs as 3 separate processes on one T4 GPU (using Time-Slicing logic). Uses **CGo** to bind to ONNX Runtime and NVML. |
| **Router** | **Your Laptop** | Load Balancing, HTTP/gRPC handling, Dashboard | Connects to workers via TCP tunnels. Uses **Weighted Random** routing based on worker scores. |
| **Dashboard** | **Your Laptop** | Visualization | Web UI on `localhost:8080` showing real-time bars/graphs. |
| **Connectivity** | **bore.pub** | Tunnels | Free reverse TCP tunnels expose Colab ports (50052-50054) to the internet so your Laptop can reach them. |

---

## 📖 The Story: What We Built & Solved

### 1. The Challenge
We needed a system that could **intelligently route** requests to GPU workers based on load, but we didn't have local GPUs. Google Colab offers free T4 GPUs, but they are ephemeral and behind firewalls.

### 2. The Problems & Solutions
| Problem | Solution |
|---------|----------|
| **No Public IP for Colab** | We used **`bore`** (a free, open-source TCP tunnel) to expose Colab ports (50052-50054) to the public internet, allowing your local Router to connect. |
| **Fake GPU Metrics** | Initially, we simulated metrics. We replaced simulation with **Real NVML Bindings (CGo)** using `libnvidia-ml.so` to read actual VRAM/Temp from the T4. |
| **Fake AI Inference** | Initially, we used `time.Sleep`. We replaced it with **Real ONNX Runtime (CGo)** to run ResNet-50 inference. |
| **Timeouts & Latency** | Routing over the internet adds ~200ms latency. Default gRPC/Poller timeouts (200ms) caused failures. We increased timeouts to **5s (Poller)** and **10s (Router/LoadTest)**. |
| **CUDA Compatibility** | **CRITICAL:** Colab upgraded to CUDA 13, but the ONNX Runtime C library v1.17 only supported CUDA 11/12. This caused silent CPU fallback (0% GPU Usage). **Fix:** We updated `colab_workers.sh` to install `onnxruntime-gpu` via pip (which supports CUDA 13) and link Go against those libraries dynamically. |

---

## 🛠️ Current Codebase State

### 1. The "Golden" Script: `scripts/colab_workers.sh`
This is the **most important file**. It automates the entire Colab setup.
- **What it does:**
    1. Installs Go 1.24, Protoc, and `bore` tunnel.
    2. **CRITICAL FIX:** Installs `onnxruntime-gpu` via pip.
    3. **CRITICAL FIX:** Dynamically finds the pip-installed `libonnxruntime_providers_cuda.so` (compatible with Colab's CUDA 13) and links your Go binary against it. *Previous method of downloading v1.17 static libs failed because they were for CUDA 11, causing CPU fallback.*
    4. Builds the `worker` binary with `-tags "onnx,nvml"`.
    5. Starts 3 workers + 3 bore tunnels.
    6. Prints the `bore.pub` addresses you need.

### 2. Timeouts & Latency Handling
Since we are routing over the internet (Laptop → bore.pub → Colab), latency is higher (~200ms). We fixed 3 specific timeouts:
- **Poller:** `pkg/router/poller.go` — Increased poll timeout from 200ms → **5s**.
- **Router Forwarding:** `pkg/router/router.go` — Middleware now creates a fresh **10s context** for forwarding instead of inheriting the client's potentially short context.
- **Load Test:** `scripts/loadtest.go` — Uses a **10s per-request timeout** instead of sharing the global test context (which was causing cascading "Deadline Exceeded" errors).

### 3. Real GPU Bindings
- **Inference:** `pkg/worker/executor/onnx.go` uses CGo to call ONNX Runtime C API.
- **Metrics:** `pkg/worker/nvml/nvml.go` uses CGo / dynamic loading to call `libnvidia-ml.so` for real VRAM/Temp usage.

---

## 📋 Operational Guide: How to Resume

**Step 1: Reset & Start on Colab**
1. Open your Google Colab notebook (ensure Runtime is **T4 GPU**).
2. Run this block to clean up and start fresh:
   ```bash
   # Kill any old processes
   !pkill -f "bin/worker" 2>/dev/null; pkill -f bore 2>/dev/null
   
   # Update code & Run the Master Script
   %cd /content/GPU-Aware-Batch-Router
   !git pull
   !bash scripts/colab_workers.sh
   ```
3. **Wait** for the output. It will eventually print:
   ```text
   Worker-1  →  bore.pub:12345
   Worker-2  →  bore.pub:67890
   Worker-3  →  bore.pub:13579
   ```
   **COPY THESE ADDRESSES.**

**Step 2: Start Router on Laptop**
1. Open terminal on your laptop:
   ```bash
   cd ~/Desktop/demo_yc_!
   git pull
   ```
2. Run the router with the addresses you copied:
   ```bash
   # Replace with ACTUAL ports from Colab
   WORKER_ENDPOINTS=bore.pub:12345,bore.pub:67890,bore.pub:13579 go run ./cmd/router/
   ```

**Step 3: Verification (The "Is it Real?" Check)**
1. **Open Dashboard:** http://localhost:8080
2. **Fire Load:**
   ```bash
   # On Laptop, different terminal
   go run scripts/loadtest.go --addr=localhost:50051 --concurrency=10 --duration=30s
   ```
3. **Verify GPU Usage (On Colab):**
   While the load test runs, verify the GPU graph spikes in Colab's "Resources" tab, or run:
   ```bash
   !nvidia-smi -l 1
   ```
   *Expectation:* GPU-Util should go > 0% and Memory Usage should increase. If it stays 0%, the "pip library fix" didn't catch, and it fell back to CPU.

---

## 🐛 Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| **"Unhealthy" Workers** | Bore tunnel connection died or timeout. | Restart router on laptop. If that fails, restart tunnels on Colab (re-run script). |
| **GPU Memory 0 MiB** | ONNX Runtime fell back to CPU execution. | Ensure `scripts/colab_workers.sh` is using the `PIP_ORT_PATH` logic. Run the verification python script to confirm CUDA works on the machine. |
| **Latency > 1s** | You are running on CPU, not GPU. | Same as above — check `!head -10 /tmp/worker-1.log`. It should say `Executor: onnx-gpu`. |
| **"Deadline Exceeded"** | Tunnels are slow. | We already fixed timeouts (10s), but if internet is terrible, you might need to increase `metrics.go` poll timeout even more. |

---

## ⏭️ Conclusion
We have successfully built a distributed, GPU-aware inference system that runs on real hardware (Colab T4) with a local control plane. All major technical hurdles (networking, latency, CUDA compatibility) have been solved. The system is ready for demo.

**Code is live at:** `https://github.com/Kunal1522/GPU-Aware-Batch-Router`
