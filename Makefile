.PHONY: up down test lint cover front-test front-lint

up:
	docker compose up -d --build

down:
	docker compose down

test:
	cd back && go test -race ./...

lint:
	cd back && golangci-lint fmt && golangci-lint run

cover:
	cd back && ./scripts/coverage.sh

front-test:
	cd front && npm run test:coverage

front-lint:
	cd front && npm run lint && npx tsc -b
