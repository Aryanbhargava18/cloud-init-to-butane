.PHONY: build test clean fmt

build:
	go build -o bin/cloud-init-to-butane ./cmd/cloud-init-to-butane

test:
	go test -v ./...

clean:
	rm -rf bin/

fmt:
	go fmt ./...
