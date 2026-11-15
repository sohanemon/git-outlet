.PHONY: build clean install test uninstall

BIN := git-outlet

build:
	go build -trimpath -ldflags="-s -w" -o bin/$(BIN) ./cmd/$(BIN)

clean:
	rm -rf bin

install:
	go install -trimpath -ldflags="-s -w" ./cmd/$(BIN)

test:
	go test ./...

GO_BIN := $(shell go env GOBIN)
ifeq ($(GO_BIN),)
GO_BIN := $(shell go env GOPATH)/bin
endif

uninstall:
	rm -f "$(GO_BIN)/$(BIN)"
