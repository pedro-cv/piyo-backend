# Piyo API developer tasks. Run from backend/.

GO ?= go
BIN ?= bin/piyo-api

.PHONY: run build fmt vet tidy clean

## run: start the API in DEV_MODE (in-memory store + seed data)
run:
	$(GO) run ./cmd/piyo-api

## build: compile the binary into bin/
build:
	$(GO) build -o $(BIN) ./cmd/piyo-api

## fmt: format all Go files
fmt:
	gofmt -w .

## vet: static checks
vet:
	$(GO) vet ./...

## tidy: sync go.mod/go.sum
tidy:
	$(GO) mod tidy

clean:
	rm -rf bin
