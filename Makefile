VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/nickelsey/storied/internal/cli.Version=$(VERSION)
PLATFORMS := darwin/arm64 linux/amd64 windows/amd64

.PHONY: build test vet fmt check dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/storied ./cmd/storied

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# What CI runs.
check: vet test
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

# Static binaries for every platform the pilot uses.
dist:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" \
			-o dist/storied-$$os-$$arch$$ext ./cmd/storied || exit 1; \
	done

clean:
	rm -rf bin dist
