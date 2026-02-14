#!/bin/bash
set -euo pipefail

# Generate Go code from protobuf definitions
# Requires: protoc, protoc-gen-go, protoc-gen-go-grpc

PROTO_DIR="proto"
MODULE="github.com/kunal/gpu-batch-router"

PATH=$HOME/.local/bin:$HOME/go/bin:$PATH

mkdir -p gen/inference/v1

protoc \
  --proto_path="$PROTO_DIR" \
  --go_out=. \
  --go_opt=module="$MODULE" \
  --go-grpc_out=. \
  --go-grpc_opt=module="$MODULE" \
  inference/v1/inference.proto

echo "✅ Proto generation complete → gen/inference/v1/"
