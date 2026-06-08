.PHONY: all build run test clean docker-up docker-down gen-api verify-api

# Default target
all: build

# Generate code from OpenAPI spec
gen-api:
	@echo "Generating API code from OpenAPI spec..."
	mkdir -p internal/api api
	cp ../architecture/openapi.yaml api/openapi.yaml
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 \
		-config codegen.yaml api/openapi.yaml

# Verify OpenAPI codegen has not drifted (CI Guard)
verify-api:
	@echo "Verifying API code drift..."
	mkdir -p internal/api api
	cp ../architecture/openapi.yaml api/openapi.yaml
	# Generate to a temporary file
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 \
		-config codegen.yaml api/openapi.yaml
	# Check if git detects any diff in internal/api/generated.go
	git diff --exit-code internal/api/generated.go

# Build local binary
build: gen-api
	@echo "Building Go API..."
	go build -o bin/server cmd/server/main.go

# Run locally
run: build
	@echo "Running API locally..."
	./bin/server

# Run tests
test:
	@echo "Running tests..."
	go test -p 1 -v ./...

# Stop and clean up containers
docker-down:
	docker compose down -v

# Start containers and rebuild app
docker-up:
	docker compose up --build -d

# Clean build artifacts
clean:
	rm -rf bin/ internal/api/generated.go api/openapi.yaml
