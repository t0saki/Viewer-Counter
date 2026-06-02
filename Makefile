.PHONY: build run tidy vet test docker-build compose-up compose-down

build:
	go build -trimpath -o bin/viewer-counter ./cmd/server

run:
	go run ./cmd/server -config config.yaml

tidy:
	go mod tidy

vet:
	go vet ./...

test:
	go test ./...

docker-build:
	docker build -t viewer-counter:latest .

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down
