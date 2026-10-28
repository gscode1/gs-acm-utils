VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/gscode1/gs-acm-utils/internal/cli.version=$(VERSION)
BIN     := bin/acm-mock-resolver
DIST    := dist
# os/arch pairs built by `make release`.
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64

.PHONY: build test lint fmt bench tidy release clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/acm-mock-resolver

test:
	go test -race ./...

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

bench:
	go test -run '^$$' -bench . -benchmem -benchtime 200x ./internal/resolve

tidy:
	go mod tidy

# release cross-compiles every platform into dist/, one tar.gz each, plus
# sha256sums.txt. VERSION comes from the git tag (git describe); run it on a
# tagged, clean checkout. Upload with: gh release create vX.Y.Z dist/*
release: clean
	@case "$(VERSION)" in *-dirty|dev) echo "refusing to release version '$(VERSION)': use a clean tagged checkout"; exit 1;; esac
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		name=acm-mock-resolver_$(VERSION)_$${os}_$${arch}; \
		echo "building $$name"; \
		mkdir -p $(DIST)/$$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/$$name/acm-mock-resolver ./cmd/acm-mock-resolver; \
		cp LICENSE NOTICE README.md $(DIST)/$$name/; \
		tar -C $(DIST) -czf $(DIST)/$$name.tar.gz $$name; \
		rm -rf $(DIST)/$$name; \
	done
	@cd $(DIST) && (command -v sha256sum >/dev/null && sha256sum *.tar.gz || shasum -a 256 *.tar.gz) > sha256sums.txt
	@cat $(DIST)/sha256sums.txt

clean:
	rm -rf $(DIST)
