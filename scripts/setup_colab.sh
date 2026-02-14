#!/bin/bash
# =============================================================================
# GPU Batch Router — Colab Setup Script
# Run this in a Colab notebook cell: !bash scripts/setup_colab.sh
#
# NOTE: Colab doesn't have systemd, so we skip K3s and run directly.
# =============================================================================
set -euo pipefail

echo "═══════════════════════════════════════════════════════════"
echo "🚀 GPU-Aware Intelligent Batch Router — Colab Setup"
echo "═══════════════════════════════════════════════════════════"

# --- Step 1: Verify GPU ---
echo ""
echo "📌 Step 1: Checking GPU..."
if ! nvidia-smi &>/dev/null; then
    echo "❌ No GPU detected! Go to Runtime → Change runtime type → T4 GPU"
    exit 1
fi
GPU_NAME=$(nvidia-smi --query-gpu=gpu_name --format=csv,noheader | head -1)
GPU_VRAM=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader | head -1)
echo "✅ GPU detected: $GPU_NAME ($GPU_VRAM)"

# --- Step 2: Install Go ---
echo ""
echo "📌 Step 2: Installing Go 1.24..."
if ! command -v /usr/local/go/bin/go &>/dev/null; then
    wget -q https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
    rm go1.24.0.linux-amd64.tar.gz
fi
export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH
echo "✅ Go $(/usr/local/go/bin/go version | awk '{print $3}')"

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

# --- Step 4: Install ONNX Runtime C library ---
echo ""
echo "📌 Step 4: Installing ONNX Runtime (GPU)..."
ONNX_VERSION="1.17.0"
if [ ! -d "/usr/local/onnxruntime" ]; then
    wget -q "https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-gpu-${ONNX_VERSION}.tgz" \
         -O onnxruntime.tgz
    sudo mkdir -p /usr/local/onnxruntime
    sudo tar -xzf onnxruntime.tgz -C /usr/local/onnxruntime --strip-components=1
    rm onnxruntime.tgz
    echo "/usr/local/onnxruntime/lib" | sudo tee /etc/ld.so.conf.d/onnxruntime.conf >/dev/null
    sudo ldconfig
fi
export CGO_CFLAGS="-I/usr/local/onnxruntime/include"
export CGO_LDFLAGS="-L/usr/local/onnxruntime/lib -lonnxruntime"
export LD_LIBRARY_PATH="/usr/local/onnxruntime/lib:${LD_LIBRARY_PATH:-}"
echo "✅ ONNX Runtime ${ONNX_VERSION} installed"

# --- Step 5: Download ResNet-50 Model ---
echo ""
echo "📌 Step 5: Downloading ResNet-50 ONNX model..."
mkdir -p models
if [ ! -f "models/resnet50.onnx" ]; then
    wget -q "https://github.com/onnx/models/raw/main/validated/vision/classification/resnet/model/resnet50-v2-7.onnx" \
         -O models/resnet50.onnx || {
        echo "⚠️  Primary download failed, trying mirror..."
        pip install -q onnx >/dev/null 2>&1
        python3 -c "
import urllib.request
url = 'https://huggingface.co/onnx-community/resnet-50/resolve/main/model.onnx'
urllib.request.urlretrieve(url, 'models/resnet50.onnx')
print('Downloaded from HuggingFace')
" || echo "⚠️  Model download failed — will use simulation fallback"
    }
fi
if [ -f "models/resnet50.onnx" ]; then
    echo "✅ ResNet-50 model ready ($(du -h models/resnet50.onnx | awk '{print $1}'))"
else
    echo "⚠️  No model found — workers will use simulation executor"
fi

# --- Step 6: Generate proto + Build ---
echo ""
echo "📌 Step 6: Building binaries..."
bash scripts/gen-proto.sh
go mod tidy

# Try building with ONNX+NVML tags; fall back to default if CGo fails
echo "   Attempting GPU build (onnx + nvml)..."
if CGO_ENABLED=1 go build -tags "onnx,nvml" -o bin/worker ./cmd/worker/ 2>/dev/null; then
    echo "   ✅ Worker built with REAL ONNX + NVML"
    WORKER_BIN="bin/worker"
    EXECUTOR_TYPE="onnx"
else
    echo "   ⚠️  CGo build failed — building with simulation"
    go build -o bin/worker ./cmd/worker/
    WORKER_BIN="bin/worker"
    EXECUTOR_TYPE="simulation"
fi

go build -o bin/router ./cmd/router/
echo "✅ Binaries built:"
ls -la bin/

# --- Step 7: Start Workers (3 instances) ---
echo ""
echo "📌 Step 7: Starting 3 GPU workers..."

# Kill any existing processes
pkill -f "bin/worker" 2>/dev/null || true
pkill -f "bin/router" 2>/dev/null || true
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
    nohup ./${WORKER_BIN} > /tmp/worker-${i}.log 2>&1 &
    
    echo "   ⚡ Worker-${i} started on :${GRPC_PORT} (metrics :${METRICS_PORT})"
done
sleep 3

# Verify workers are up
for i in 1 2 3; do
    PORT=$((50051 + i))
    if ss -tlnp | grep -q ":${PORT}" 2>/dev/null; then
        echo "   ✅ Worker-${i} listening on :${PORT}"
    else
        echo "   ❌ Worker-${i} failed! Check: cat /tmp/worker-${i}.log"
    fi
done

# --- Step 8: Start Router ---
echo ""
echo "📌 Step 8: Starting router..."

ROUTER_PORT=50051 \
DASHBOARD_PORT=8080 \
WORKER_ENDPOINTS="localhost:50052,localhost:50053,localhost:50054" \
POLL_INTERVAL_MS=500 \
nohup ./bin/router > /tmp/router.log 2>&1 &

sleep 2

if ss -tlnp | grep -q ":8080" 2>/dev/null; then
    echo "   ✅ Router started on :50051"
    echo "   ✅ Dashboard on :8080"
else
    echo "   ❌ Router failed! Check: cat /tmp/router.log"
fi

# --- Step 9: Expose Dashboard ---
echo ""
echo "📌 Step 9: Exposing dashboard..."

pip install -q pyngrok 2>/dev/null

python3 -c "
from pyngrok import ngrok
try:
    tunnel = ngrok.connect(8080)
    print(f'')
    print(f'🌐 ═══════════════════════════════════════════')
    print(f'🌐  DASHBOARD: {tunnel.public_url}')
    print(f'🌐 ═══════════════════════════════════════════')
    print(f'')
except Exception as e:
    print(f'⚠️  ngrok failed: {e}')
    print(f'   Dashboard available locally on port 8080')
" 2>/dev/null || echo "   Dashboard on port 8080 (install pyngrok for public URL)"

# --- Step 10: Show GPU status ---
echo ""
echo "📌 Step 10: Current GPU status..."
nvidia-smi

# --- Done! ---
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "🎉 GPU Batch Router is LIVE!"
echo ""
echo "   📊 Run load test:"
echo "      go run scripts/loadtest.go --addr=localhost:50051 --concurrency=50 --duration=30s"
echo ""
echo "   📋 Check logs:"
echo "      cat /tmp/worker-1.log"
echo "      cat /tmp/router.log"
echo ""
echo "   🔍 GPU monitoring:"
echo "      nvidia-smi -l 1"
echo "═══════════════════════════════════════════════════════════"
