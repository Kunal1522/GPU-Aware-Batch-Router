#!/bin/bash
# =============================================================================
# GPU Batch Router — ALL-IN-ONE Colab Setup
#
# Runs workers + router + dashboard on Colab.
# View dashboard using Colab proxy (no ngrok needed).
#
# Usage:
#   !bash scripts/setup_colab.sh
#
# Then in next cell:
#   from google.colab.output import eval_js
#   print(eval_js("google.colab.kernel.proxyPort(8080)"))
# =============================================================================
set -euo pipefail

echo "═══════════════════════════════════════════════════════════"
echo "🚀 GPU-Aware Batch Router — Colab Setup"
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
         -O models/resnet50.onnx 2>/dev/null || echo "⚠️  Download failed, using simulation"
fi
[ -f "models/resnet50.onnx" ] && echo "✅ ResNet-50 ready"

# --- Step 6: Build ---
echo ""
echo "📌 Step 6: Building..."
bash scripts/gen-proto.sh
go mod tidy 2>/dev/null

EXECUTOR_TYPE="simulation"
if CGO_ENABLED=1 go build -tags "onnx,nvml" -o bin/worker ./cmd/worker/ 2>/dev/null; then
    echo "✅ Worker: REAL ONNX + NVML"
    EXECUTOR_TYPE="onnx"
else
    go build -o bin/worker ./cmd/worker/
    echo "✅ Worker: simulation mode"
fi
go build -o bin/router ./cmd/router/
echo "✅ Router built"

# --- Step 7: Kill old processes & start fresh ---
echo ""
echo "📌 Step 7: Starting services..."
pkill -f "bin/worker" 2>/dev/null || true
pkill -f "bin/router" 2>/dev/null || true
sleep 1

export ONNX_MODEL_PATH="$(pwd)/models/resnet50.onnx"

for i in 1 2 3; do
    WORKER_ID="worker-${i}" \
    WORKER_PORT="$((50051 + i))" \
    METRICS_PORT="$((9090 + i))" \
    MAX_BATCH_SIZE=32 \
    MAX_WAIT_MS=50 \
    EXECUTOR_TYPE="${EXECUTOR_TYPE}" \
    USE_NVML=true \
    LD_LIBRARY_PATH="/usr/local/onnxruntime/lib:${LD_LIBRARY_PATH:-}" \
    nohup ./bin/worker > /tmp/worker-${i}.log 2>&1 &
    echo "   ⚡ Worker-${i} on :$((50051 + i))"
done
sleep 3

ROUTER_PORT=50051 \
DASHBOARD_PORT=8080 \
WORKER_ENDPOINTS="localhost:50052,localhost:50053,localhost:50054" \
POLL_INTERVAL_MS=500 \
nohup ./bin/router > /tmp/router.log 2>&1 &
sleep 2

echo "   ✅ Router on :50051"
echo "   ✅ Dashboard on :8080"

# --- Done ---
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "🎉 EVERYTHING IS RUNNING!"
echo ""
echo "📊 To see the dashboard, run this in the NEXT cell:"
echo ""
echo '   from google.colab.output import eval_js'
echo '   url = eval_js("google.colab.kernel.proxyPort(8080)")'
echo '   print(f"👉 Open in new tab: {url}")'
echo ""
echo "🏋️ To run load test, run in another cell:"
echo '   !PATH=/usr/local/go/bin:$PATH go run scripts/loadtest.go \'
echo '       --addr=localhost:50051 --concurrency=50 --duration=30s'
echo ""
echo "═══════════════════════════════════════════════════════════"
