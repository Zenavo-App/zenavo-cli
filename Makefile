VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/Zenavo-App/zenavo-cli/internal/cli.Version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build test dist clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o zenavo ./cmd/zenavo

test:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...

# Cross-compiled binaries and a SHA256SUMS file in dist/.
dist:
	rm -rf dist && mkdir -p dist
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/zenavo_$${os}_$${arch}$$ext ./cmd/zenavo || exit 1; \
	done
	cd dist && shasum -a 256 zenavo_* > SHA256SUMS

clean:
	rm -rf zenavo dist
