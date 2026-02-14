#!/bin/bash
# =============================================================================
# GPU Batch Router — Colab WORKERS ONLY (Split Architecture)
#
# Workers run here on Colab (real T4 GPU) | Router + Dashboard run on YOUR laptop
# Uses bore.pub for free TCP tunnels (no signup)
# =============================================================================
set -euo pipefail

echo "═══════════════════════════════════════════════════════════"
echo "🚀 GPU Workers — Colab (split architecture)"
echo "═══════════════════════════════════════════════════════════"

# --- Step 1: Verify GPU ---
echo ""
echo "📌 Step 1: Checking GPU..."
if ! nvidia-smi &>/dev/null; then
    echo "❌ No GPU! Runtime → Change runtime type → T4 GPU"
    exit 1
fi
GPU_NAME=$(nvidia-smi --query-gpu=gpu_name --format=csv,noheader | head -1)
echo "✅ GPU: $GPU_NAME"

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

# --- Step 4: Install ONNX Runtime (pip version for CUDA 13) ---
echo ""
echo "📌 Step 4: Configuring ONNX Runtime..."
pip install onnxruntime-gpu -q
# Locate the pip-installed library
PIP_ORT_PATH=$(find /usr/local/lib -name "libonnxruntime.so.1.*" | grep "caret" -v | head -1 | xargs dirname)
if [ -z "$PIP_ORT_PATH" ]; then
    # Fallback search
    PIP_ORT_PATH=$(find /usr/local/lib -name "libonnxruntime_providers_cuda.so" | head -1 | xargs dirname)
fi

echo "   Found ONNX libs at: $PIP_ORT_PATH"

# Setup CGo flags to link against these libs
export CGO_CFLAGS="-I/usr/local/onnxruntime/include" # We still need headers, download them separately if missing
if [ ! -d "/usr/local/onnxruntime/include" ]; then
    wget -q "https://github.com/microsoft/onnxruntime/releases/download/v1.17.0/onnxruntime-linux-x64-gpu-1.17.0.tgz" -O onnx_headers.tgz
    tar -xzf onnx_headers.tgz
    sudo mkdir -p /usr/local/onnxruntime/include
    sudo cp -r onnxruntime-linux-x64-gpu-1.17.0/include/* /usr/local/onnxruntime/include/
    rm -rf onnxruntime-linux-x64-gpu-1.17.0 onnx_headers.tgz
fi

export CGO_LDFLAGS="-L${PIP_ORT_PATH} -lonnxruntime"
export LD_LIBRARY_PATH="${PIP_ORT_PATH}:${LD_LIBRARY_PATH:-}"
echo "✅ ONNX Runtime configured"

# --- Step 5: Download ResNet-50 ---
echo ""
echo "📌 Step 5: Downloading ResNet-50..."
mkdir -p models
if [ ! -f "models/resnet50.onnx" ]; then
    wget -q "https://github.com/onnx/models/raw/main/validated/vision/classification/resnet/model/resnet50-v2-7.onnx" \
         -O models/resnet50.onnx 2>/dev/null || echo "⚠️  Download failed"
fi
[ -f "models/resnet50.onnx" ] && echo "✅ ResNet-50 ready"

# --- Step 6: Build worker ---
echo ""
echo "📌 Step 6: Building worker..."
bash scripts/gen-proto.sh
go mod tidy 2>/dev/null

EXECUTOR_TYPE="simulation"
if CGO_ENABLED=1 go build -tags "onnx,nvml" -o bin/worker ./cmd/worker/ 2>/dev/null; then
    echo "✅ Worker: REAL ONNX + NVML"
    EXECUTOR_TYPE="onnx"
else
    # Fallback to pure simulation if CGo fails
    echo "⚠️  CGo build failed — falling back to simulation"
    go build -o bin/worker ./cmd/worker/
    EXECUTOR_TYPE="simulation"
fi

# --- Step 7: Install bore (free TCP tunnel) ---
echo ""
echo "📌 Step 7: Installing bore tunnel..."
if ! command -v bore &>/dev/null; then
    wget -q "https://github.com/ekzhang/bore/releases/download/v0.5.2/bore-v0.5.2-x86_64-unknown-linux-musl.tar.gz" \
         -O bore.tar.gz
    tar -xzf bore.tar.gz
    sudo mv bore /usr/local/bin/bore
    rm bore.tar.gz
    echo "✅ bore installed"
else
    echo "✅ bore already installed"
fi

# --- Step 8: Start workers ---
echo ""
echo "📌 Step 8: Starting 3 GPU workers..."
pkill -f "bin/worker" 2>/dev/null || true
pkill -f "bore local" 2>/dev/null || true
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
    LD_LIBRARY_PATH="${PIP_ORT_PATH}:${LD_LIBRARY_PATH:-}" \
    nohup ./bin/worker > /tmp/worker-${i}.log 2>&1 &
    echo "   ⚡ Worker-${i} on :${GRPC_PORT}"
done
sleep 3

# --- Step 9: Expose workers via bore tunnels ---
echo ""
echo "📌 Step 9: Creating bore tunnels..."
echo ""

BORE_PORTS=""
for i in 1 2 3; do
    LOCAL_PORT=$((50051 + i))
    # bore assigns a random public port on bore.pub
    nohup bore local ${LOCAL_PORT} --to bore.pub > /tmp/bore-${i}.log 2>&1 &
    sleep 2
done

# Wait for tunnels to establish
sleep 3

echo ""
echo "═══════════════════════════════════════════════════════════"
echo "🎉 WORKERS RUNNING ON REAL GPU!"
echo ""
echo "📋 Check bore tunnel addresses:"
echo ""
for i in 1 2 3; do
    PORT=$((50051 + i))
    BORE_ADDR=$(grep -oP 'bore\.pub:\d+' /tmp/bore-${i}.log 2>/dev/null | tail -1)
    if [ -n "$BORE_ADDR" ]; then
        echo "   Worker-${i}  →  ${BORE_ADDR}"
        if [ -z "$BORE_PORTS" ]; then
            BORE_PORTS="${BORE_ADDR}"
        else
            BORE_PORTS="${BORE_PORTS},${BORE_ADDR}"
        fi
    else
        echo "   Worker-${i}  →  checking... (cat /tmp/bore-${i}.log)"
    fi
done

echo ""
if [ -n "$BORE_PORTS" ]; then
    echo "═══════════════════════════════════════════════════════════"
    echo "👉 ON YOUR LAPTOP, run this command:"
    echo ""
    echo "   cd ~/Desktop/demo_yc_!"
    echo "   git pull  # Get latest code first!"
    echo "   WORKER_ENDPOINTS=${BORE_PORTS} go run ./cmd/router/"
    echo ""
    echo "Then open: http://localhost:8080"
    echo "═══════════════════════════════════════════════════════════"
else
    echo "⚠️  Bore tunnels still starting. Check addresses with:"
    echo "   !cat /tmp/bore-1.log"
    echo "   !cat /tmp/bore-2.log"
    echo "   !cat /tmp/bore-3.log"
fi
