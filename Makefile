.PHONY: up down test lint cover

up:
	docker compose up -d --build

down:
	docker compose down

test:
	go test -race ./...

lint:
	golangci-lint fmt
	golangci-lint run

cover:
	./scripts/coverage.sh
