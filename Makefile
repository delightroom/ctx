.PHONY: build test vet check test-installer install

build:
	go build -o bin/ctx ./cmd/ctx

test:
	go test ./...

vet:
	go vet ./...

test-installer:
	sh -n install.sh scripts/test-installer.sh
	sh scripts/test-installer.sh

check: test vet test-installer

install:
	go install ./cmd/ctx
