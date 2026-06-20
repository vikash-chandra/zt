# Makefile for Zerodha Trading Bot

.PHONY: help build run test clean docker-up docker-down

help:
	@echo "Available targets:"
	@echo "  make build       - Build the binary"
	@echo "  make run         - Run the bot"
	@echo "  make test        - Run tests"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make docker-up   - Start Docker containers (PostgreSQL, Redis, Prometheus)"
	@echo "  make docker-down - Stop Docker containers"

build:
	go build -o trading-bot

run: build
	./trading-bot

test:
	go test -v ./...

clean:
	rm -f trading-bot
	go clean

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

check: fmt vet lint test

dev: docker-up
	@echo "Waiting for services to be ready..."
	@sleep 5
	$(MAKE) run
