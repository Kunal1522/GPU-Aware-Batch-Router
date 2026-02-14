#!/bin/bash
# =============================================================================
# GPU Batch Router — Colab WORKERS ONLY
# 
# This script runs ONLY the GPU workers on Colab (real T4 GPU).
# Your router + dashboard runs on YOUR LOCAL machine.
#
# How it works:
#   Colab (workers) ←── ngrok tunnels ──→ Your laptop (router + dashboard)
#
# Usage in Colab:
#   !bash scripts/colab_workers.sh
# =============================================================================
set -euo pipefail

echo "═══════════════════════════════════════════════════════════"
echo "🚀 GPU Workers — Colab Setup (workers only)"
echo "═══════════════════════════════════════════════════════════"

# --- Step 1: Verify GPU ---
echo ""
echo "📌 Step 1: Checking GPU..."
if ! nvidia-smi &>/dev/null; then
    echo "❌ No GPU! Go to Runtime → Change runtime type → T4 GPU"
    exit 1
fi
GPU_NAME=$(nvidia-smi --query-gpu=gpu_name --format=csv,noheader | head -1)
GPU_VRAM=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader | head -1)
echo "✅ GPU: $GPU_NAME ($GPU_VRAM)"

# --- Step 2: Install Go ---
echo ""
echo "📌 Step 2: Installing Go..."
if ! command -v /usr/local/go/bin/go &>/dev/null; then
    wget -q https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
    rm go1.24.0.linux-amd64.tar.gz
fi
export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH
echo "✅ Go ready"

# --- Step 3: Install protoc ---
echo ""
echo "📌 Step 3: Installing protoc..."
if ! command -v protoc &>/dev/null; then
    wget -q https://github.com/protocolbuffers/protobuf/releases/download/v25.1/protoc-25.1-linux-x86_64.zip
    sudo unzip -q -o protoc-25.1-linux-x86_64.zip -d /usr/local
    rm protoc-25.1-linux-x86_64.zip
fi
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest 2>/dev/null
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest 2>/dev/null
echo "✅ protoc ready"

# --- Step 4: Install ONNX Runtime ---
echo ""
echo "📌 Step 4: Installing ONNX Runtime..."
ONNX_VERSION="1.17.0"
if [ ! -d "/usr/local/onnxruntime" ]; then
    wget -q "https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-gpu-${ONNX_VERSION}.tgz" \
         -O onnxruntime.tgz
    sudo mkdir -p /usr/local/onnxruntime
    sudo tar -xzf onnxruntime.tgz -C /usr/local/onnxruntime --strip-components=1
    rm onnxruntime.tgz
    echo "/usr/local/onnxruntime/lib" | sudo tee /etc/ld.so.conf.d/onnxruntime.conf >/dev/null
    sudo ldconfig 2>/dev/null
fi
export CGO_CFLAGS="-I/usr/local/onnxruntime/include"
export CGO_LDFLAGS="-L/usr/local/onnxruntime/lib -lonnxruntime"
export LD_LIBRARY_PATH="/usr/local/onnxruntime/lib:${LD_LIBRARY_PATH:-}"
echo "✅ ONNX Runtime ready"

# --- Step 5: Download ResNet-50 ---
echo ""
echo "📌 Step 5: Downloading ResNet-50..."
mkdir -p models
if [ ! -f "models/resnet50.onnx" ]; then
    wget -q "https://github.com/onnx/models/raw/main/validated/vision/classification/resnet/model/resnet50-v2-7.onnx" \
         -O models/resnet50.onnx 2>/dev/null || echo "⚠️  Model download failed — using simulation"
fi
[ -f "models/resnet50.onnx" ] && echo "✅ ResNet-50 ready ($(du -h models/resnet50.onnx | awk '{print $1}'))"

# --- Step 6: Build worker binary ---
echo ""
echo "📌 Step 6: Building worker binary..."
bash scripts/gen-proto.sh
go mod tidy 2>/dev/null

if CGO_ENABLED=1 go build -tags "onnx,nvml" -o bin/worker ./cmd/worker/ 2>/dev/null; then
    echo "✅ Worker built with REAL ONNX + NVML"
    EXECUTOR_TYPE="onnx"
else
    echo "⚠️  CGo failed — building simulation"
    go build -o bin/worker ./cmd/worker/
    EXECUTOR_TYPE="simulation"
fi

# --- Step 7: Start 3 workers ---
echo ""
echo "📌 Step 7: Starting 3 GPU workers..."
pkill -f "bin/worker" 2>/dev/null || true
sleep 1

export ONNX_MODEL_PATH="$(pwd)/models/resnet50.onnx"

for i in 1 2 3; do
    GRPC_PORT=$((50051 + i))
    METRICS_PORT=$((9090 + i))
    
    WORKER_ID="worker-${i}" \
    WORKER_PORT="${GRPC_PORT}" \
    METRICS_PORT="${METRICS_PORT}" \
    MAX_BATCH_SIZE=32 \
    MAX_WAIT_MS=50 \
    EXECUTOR_TYPE="${EXECUTOR_TYPE}" \
    USE_NVML=true \
    LD_LIBRARY_PATH="/usr/local/onnxruntime/lib:${LD_LIBRARY_PATH:-}" \
    nohup ./bin/worker > /tmp/worker-${i}.log 2>&1 &
    
    echo "   ⚡ Worker-${i} on :${GRPC_PORT}"
done
sleep 3

# --- Step 8: Expose workers via ngrok ---
echo ""
echo "📌 Step 8: Exposing workers via ngrok..."
pip install -q pyngrok 2>/dev/null

python3 << 'PYTHON_SCRIPT'
import json
from pyngrok import ngrok

tunnels = {}
for i in range(1, 4):
    port = 50051 + i
    try:
        tunnel = ngrok.connect(port, "tcp")
        public_url = tunnel.public_url.replace("tcp://", "")
        tunnels[f"worker-{i}"] = public_url
        print(f"   ✅ Worker-{i} (:{ port }) → {public_url}")
    except Exception as e:
        print(f"   ❌ Worker-{i} ngrok failed: {e}")

if tunnels:
    endpoints = ",".join(tunnels.values())
    print("")
    print("═══════════════════════════════════════════════════════════")
    print("🎉 WORKERS ARE LIVE ON REAL GPU!")
    print("")
    print("Copy this command and run it on YOUR LOCAL machine:")
    print("")
    print(f"   WORKER_ENDPOINTS={endpoints} \\")
    print(f"   go run ./cmd/router/")
    print("")
    print("Then open: http://localhost:8080")
    print("═══════════════════════════════════════════════════════════")
else:
    print("")
    print("❌ ngrok failed. Sign up free at https://dashboard.ngrok.com/signup")
    print("   Then run in Colab: !ngrok authtoken YOUR_TOKEN")
    print("   Then re-run: !bash scripts/colab_workers.sh")

PYTHON_SCRIPT

echo ""
echo "📋 Worker logs: cat /tmp/worker-1.log"
echo "🔍 GPU status:  nvidia-smi -l 1"
