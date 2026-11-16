.PHONY: build test test-race test-short lint vet

# Build all binaries.
build:
	go build ./...

# Run all tests with the race detector.
# -p 1 serialises package execution to prevent integration tests from
# polluting each other's shared PostgreSQL test database.
test:
	go test -race -p 1 ./...

# Same as test but skip integration tests (no database required).
test-short:
	go test -race -short ./...

# Run the race detector with verbose output.
test-verbose:
	go test -race -p 1 -v ./...

# Run go vet.
vet:
	go vet ./...
