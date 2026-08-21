# BenchCTL Makefile

.PHONY: all build clean docker-build docker-run help

all: build

# Build Go binary locally
build:
	@echo "🔨 Building benchctl..."
	go build -o benchctl ./cmd
	@echo "✅ Build complete: ./benchctl"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning..."
	rm -f benchctl
	go clean
	@echo "✅ Clean complete"

# Build Docker image
docker-build:
	@echo "🐳 Building Docker image..."
	./build.sh

# Run in Docker (hello test)
docker-run:
	@echo "🚀 Running in Docker..."
	./run.sh --test=hello

# Help
help:
	@echo "BenchCTL - Simple Benchmark Tool"
	@echo ""
	@echo "Commands:"
	@echo "  make build         Build Go binary"
	@echo "  make clean         Remove build artifacts"
	@echo "  make docker-build  Build Docker image"
	@echo "  make docker-run    Run hello test in Docker"
	@echo ""
	@echo "Kubernetes:"
	@echo "  ./build.sh                           # Build image"
	@echo "  kind load docker-image benchctl:latest  # Load to kind"
	@echo "  kubectl apply -f pod.yaml            # Run as pod"
	@echo "  kubectl logs benchctl-hello -f       # View logs"
