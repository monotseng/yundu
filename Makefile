.PHONY: web build build-all test release-package

TARGET_OS ?= linux
TARGET_ARCH ?= $(shell go env GOARCH)
VERSION ?= $(shell date -u +%Y%m%d%H%M%S)

web:
	cd web && npm ci && npm run build
	rm -rf internal/web/dist
	cp -a web/dist internal/web/dist

build: web
	mkdir -p release
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o release/yundu-$(TARGET_OS)-$(TARGET_ARCH) ./cmd/yundu-web

build-all: web
	$(MAKE) build TARGET_OS=linux TARGET_ARCH=amd64 VERSION=$(VERSION)
	$(MAKE) build TARGET_OS=linux TARGET_ARCH=arm64 VERSION=$(VERSION)

test:
	go test ./...
	cd web && npm run typecheck

release-package:
	@test -n "$(VERSION)" || (echo "VERSION is required, e.g. make release-package VERSION=1.0.0-rc.1"; exit 2)
	bash scripts/package-release.sh "$(VERSION)"
