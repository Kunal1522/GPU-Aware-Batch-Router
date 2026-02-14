.PHONY: proto build test lint docker compose-up compose-down loadtest clean

# ─── Proto Generation ───────────────────────────────────────
proto:
	@echo "🔧 Generating protobuf code..."
	@bash scripts/gen-proto.sh

# ─── Build ──────────────────────────────────────────────────
build: proto
	@echo "🔨 Building router..."
	go build -o bin/router ./cmd/router/
	@echo "🔨 Building worker..."
	go build -o bin/worker ./cmd/worker/
	@echo "✅ Binaries in ./bin/"

# ─── Test ───────────────────────────────────────────────────
test:
	@echo "🧪 Running tests..."
	go test -v -race -count=1 ./...

test-short:
	go test -short -race ./...

# ─── Lint ───────────────────────────────────────────────────
lint:
	golangci-lint run ./...

# ─── Docker ─────────────────────────────────────────────────
docker:
	docker build -f deploy/docker/Dockerfile.router -t gpu-router:latest .
	docker build -f deploy/docker/Dockerfile.worker -t gpu-worker:latest .

# ─── Docker Compose (local dev) ────────────────────────────
compose-up:
	docker-compose -f deploy/docker-compose.yaml up --build -d

compose-down:
	docker-compose -f deploy/docker-compose.yaml down

# ─── Load Test ──────────────────────────────────────────────
loadtest:
	go run scripts/loadtest.go \
		--addr=localhost:50051 \
		--concurrency=100 \
		--duration=30s

# ─── K3s Deploy (Colab) ────────────────────────────────────
k3s-deploy:
	kubectl apply -f deploy/k8s/

k3s-status:
	kubectl get pods -o wide
	kubectl get svc

# ─── Clean ──────────────────────────────────────────────────
clean:
	rm -rf bin/ gen/
