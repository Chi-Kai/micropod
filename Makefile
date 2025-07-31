.PHONY: generate build build-agent clean install-protoc

# Generate protobuf Go code
generate:
	protoc --go_out=. --go-grpc_out=. pkg/agent/api/agent.proto

# Build micropod binary
build:
	go build -o bin/micropod cmd/micropod/main.go

# Build agent binary for guest VM
build-agent:
	CGO_ENABLED=0 GOOS=linux go build -o bin/agent cmd/agent/main.go

# Install protoc dependencies
install-protoc:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Build agent rootfs using shell script
build-rootfs:
	./scripts/build-agent-rootfs.sh

# Build agent rootfs using Docker (alternative)
build-rootfs-docker:
	make build-agent
	docker build -f Dockerfile.agent -t micropod-agent .
	docker save micropod-agent | docker run --rm -i alpine sh -c 'cat > /tmp/agent.tar && mkdir -p /tmp/rootfs && tar -xf /tmp/agent.tar -C /tmp/rootfs --strip-components=1 && tar -czf - /tmp/rootfs' > agent-rootfs.tar.gz

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f pkg/agent/api/*.pb.go
	rm -f agent-rootfs.ext4
	rm -f agent-rootfs.tar.gz

# Update dependencies
deps:
	go get google.golang.org/grpc
	go get github.com/mdlayher/vsock
	go mod tidy