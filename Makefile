.PHONY: up down test lint cover front-test front-lint check-isolation

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

# Against the running stack: what the UI opens cannot reach the API.
check-isolation:
	front/scripts/check-isolation.sh
