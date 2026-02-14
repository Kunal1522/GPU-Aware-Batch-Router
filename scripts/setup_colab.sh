#!/bin/bash
# =============================================================================
# GPU Batch Router — Colab Setup Script
# Run this in a Colab notebook cell: !bash setup_colab.sh
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
nvidia-smi

# --- Step 2: Install Go ---
echo ""
echo "📌 Step 2: Installing Go 1.24..."
if ! command -v go &>/dev/null; then
    wget -q https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
    sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
    rm go1.24.0.linux-amd64.tar.gz
fi
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
echo "✅ Go $(go version | awk '{print $3}')"

# --- Step 3: Install K3s ---
echo ""
echo "📌 Step 3: Installing K3s (lightweight Kubernetes)..."
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--disable traefik" sh -s - --write-kubeconfig-mode 644
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
echo "✅ K3s installed"
kubectl get nodes

# --- Step 4: NVIDIA GPU Time-Slicing ---
echo ""
echo "📌 Step 4: Setting up GPU Time-Slicing (1 T4 → 3 vGPUs)..."

# Install NVIDIA container toolkit
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list | \
    sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
    sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list > /dev/null
sudo apt-get update -qq && sudo apt-get install -y -qq nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=containerd
sudo systemctl restart containerd

# Apply NVIDIA device plugin with time-slicing
kubectl apply -f deploy/k8s/nvidia-device-plugin-timeslice.yaml
echo "⏳ Waiting for NVIDIA device plugin..."
kubectl -n kube-system rollout status daemonset/nvidia-device-plugin --timeout=120s || true
sleep 10

# Verify time-sliced GPUs
echo "✅ GPU resources available:"
kubectl get nodes -o json | jq '.items[0].status.allocatable["nvidia.com/gpu"]'

# --- Step 5: Install ONNX Runtime ---
echo ""
echo "📌 Step 5: Installing ONNX Runtime (GPU)..."
pip install onnxruntime-gpu 2>/dev/null || true

# Download ONNX Runtime C library for Go CGo bindings
ONNX_VERSION="1.17.0"
if [ ! -d "/usr/local/onnxruntime" ]; then
    wget -q "https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-gpu-${ONNX_VERSION}.tgz"
    sudo mkdir -p /usr/local/onnxruntime
    sudo tar -xzf "onnxruntime-linux-x64-gpu-${ONNX_VERSION}.tgz" -C /usr/local/onnxruntime --strip-components=1
    rm "onnxruntime-linux-x64-gpu-${ONNX_VERSION}.tgz"
    echo "/usr/local/onnxruntime/lib" | sudo tee /etc/ld.so.conf.d/onnxruntime.conf
    sudo ldconfig
fi
export CGO_CFLAGS="-I/usr/local/onnxruntime/include"
export CGO_LDFLAGS="-L/usr/local/onnxruntime/lib"
echo "✅ ONNX Runtime ${ONNX_VERSION} installed"

# --- Step 6: Download ResNet-50 Model ---
echo ""
echo "📌 Step 6: Downloading ResNet-50 ONNX model..."
mkdir -p models
if [ ! -f "models/resnet50.onnx" ]; then
    wget -q "https://github.com/onnx/models/raw/main/validated/vision/classification/resnet/model/resnet50-v2-7.onnx" \
         -O models/resnet50.onnx
fi
echo "✅ ResNet-50 model ready ($(du -h models/resnet50.onnx | awk '{print $1}'))"

# --- Step 7: Install protoc ---
echo ""
echo "📌 Step 7: Installing protoc..."
if ! command -v protoc &>/dev/null; then
    wget -q https://github.com/protocolbuffers/protobuf/releases/download/v25.1/protoc-25.1-linux-x86_64.zip
    sudo unzip -q -o protoc-25.1-linux-x86_64.zip -d /usr/local
    rm protoc-25.1-linux-x86_64.zip
fi
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
echo "✅ protoc $(protoc --version)"

# --- Step 8: Build with real GPU support ---
echo ""
echo "📌 Step 8: Building binaries with ONNX + NVML support..."
bash scripts/gen-proto.sh
CGO_ENABLED=1 go build -tags "onnx,nvml" -o bin/router ./cmd/router/
CGO_ENABLED=1 go build -tags "onnx,nvml" -o bin/worker ./cmd/worker/
echo "✅ Binaries built:"
ls -la bin/

# --- Step 9: Build Docker images ---
echo ""
echo "📌 Step 9: Building Docker images for K3s..."
# K3s uses containerd, import images directly
sudo docker build -t gpu-router:latest -f deploy/docker/Dockerfile.router .
sudo docker build -t gpu-worker:latest -f deploy/docker/Dockerfile.worker .
sudo k3s ctr images import <(sudo docker save gpu-router:latest)
sudo k3s ctr images import <(sudo docker save gpu-worker:latest)
echo "✅ Docker images loaded into K3s"

# --- Step 10: Deploy to K3s ---
echo ""
echo "📌 Step 10: Deploying to K3s..."
kubectl apply -f deploy/k8s/cluster.yaml
echo "⏳ Waiting for pods to be ready..."
kubectl rollout status deployment/router --timeout=120s
kubectl rollout status statefulset/worker --timeout=120s
echo ""
echo "✅ Deployment complete!"
kubectl get pods -o wide

# --- Step 11: Expose dashboard via ngrok ---
echo ""
echo "📌 Step 11: Exposing dashboard..."
pip install -q pyngrok 2>/dev/null || true

# Port-forward dashboard
kubectl port-forward svc/router 8080:8080 &
sleep 2

python3 -c "
from pyngrok import ngrok
tunnel = ngrok.connect(8080) 
print(f'🌐 Dashboard URL: {tunnel.public_url}')
print(f'   (Share this URL to view the live dashboard)')
" || echo "   Dashboard available at: kubectl port-forward svc/router 8080:8080"

# --- Step 12: Run load test ---
echo ""
echo "📌 Step 12: Running load test (10s, 50 clients)..."
kubectl port-forward svc/router 50051:50051 &
sleep 2
go run scripts/loadtest.go --addr=localhost:50051 --concurrency=50 --duration=10s

echo ""
echo "═══════════════════════════════════════════════════════════"
echo "🎉 GPU Batch Router is LIVE!"
echo ""
echo "   📊 Dashboard: Check ngrok URL above"
echo "   🔧 Monitor:   kubectl get pods -w"
echo "   📈 GPU:       nvidia-smi -l 1"
echo "   🏋️ Load test: go run scripts/loadtest.go --addr=localhost:50051 --concurrency=100 --duration=60s"
echo "═══════════════════════════════════════════════════════════"
