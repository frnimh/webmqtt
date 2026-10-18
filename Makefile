.PHONY: build test vet lint fmt
build:
	go build -o bin/webmqtt ./cmd/webmqtt
test:
	go test -race ./...
vet:
	go vet ./...
lint:
	golangci-lint run ./...
fmt:
	gofmt -w cmd internal
