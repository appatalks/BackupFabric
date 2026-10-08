.PHONY: build test fmt check mock

build:
	go build ./cmd/backupfabric ./cmd/backupfabric-mock

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -type f)

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...

mock:
	go run ./cmd/backupfabric-mock --root ./var/mock
