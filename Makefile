BINARY := bin/hexlet-go-crawler

.PHONY: build test run fmt lint tidy clean

build:
	go build -o $(BINARY) ./cmd/hexlet-go-crawler

fmt:
	golangci-lint fmt

lint: fmt
	golangci-lint run

test:
	go test ./...

run:
	@test -n "$(URL)" || { echo "usage: make run URL=<url>" >&2; exit 1; }
	go run ./cmd/hexlet-go-crawler "$(URL)"

tidy:
	go mod tidy

clean:
	rm -rf bin tmp coverage.out coverage.html
