BINARY := bin/hexlet-go-crawler
COVERAGE_PROFILE := coverage.out

.PHONY: build test cover cover-show run fmt lint tidy clean

build:
	go build -o $(BINARY) ./cmd/hexlet-go-crawler

fmt:
	golangci-lint fmt

lint: fmt
	golangci-lint run

test:
	go test ./...

cover:
	go test ./... -coverprofile=$(COVERAGE_PROFILE) -covermode=count
	go tool cover -func=$(COVERAGE_PROFILE)

cover-show: cover
	go tool cover -html=$(COVERAGE_PROFILE)

run:
	@test -n "$(URL)" || { echo "usage: make run URL=<url>" >&2; exit 1; }
	go run ./cmd/hexlet-go-crawler "$(URL)"

tidy:
	go mod tidy

clean:
	rm -rf bin tmp coverage.out
