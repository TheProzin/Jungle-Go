.PHONY: run build fmt vet test test-race compose-up compose-down compose-logs migrate-up migrate-down migrate-version test-integration

run:
	go run ./cmd/app

build:
	go build -o bin/app ./cmd/app

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

compose-up:
	docker compose up --build

compose-down:
	docker compose down -v

compose-logs:
	docker compose logs -f app

migrate-up:
	docker compose run --rm migrate up

migrate-down:
	docker compose run --rm migrate down 1

migrate-version:
	docker compose run --rm migrate version

test-integration:
	go test -tags=integration ./test/integration/...
