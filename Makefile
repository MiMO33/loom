ifneq ($(shell uname -s),Darwin)
  $(error Loom is a macOS-only desktop application)
endif

GO ?= $(or $(shell command -v go 2>/dev/null),$(CURDIR)/.tooling/go/bin/go)
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | tr -cd 'A-Za-z0-9._+-'),0.1.0)
LDFLAGS  = -s -w -X main.version=$(VERSION)

export CGO_CFLAGS  = -mmacosx-version-min=12.0
export CGO_LDFLAGS = -mmacosx-version-min=12.0

.PHONY: all ui icons build app test dev clean

all: app

ui:
	npm run build:ui

icons:
	@$(GO) run build/icon/gen.go tray internal/gui/tray.png
	@$(GO) run build/icon/gen.go app 1024 internal/gui/icon-1024.png
	@rm -rf build/darwin/loom.iconset && mkdir -p build/darwin/loom.iconset
	@for s in 16 32 128 256 512; do \
		$(GO) run build/icon/gen.go app $$s build/darwin/loom.iconset/icon_$${s}x$${s}.png; \
		$(GO) run build/icon/gen.go app $$((s*2)) build/darwin/loom.iconset/icon_$${s}x$${s}@2x.png; \
	done
	@iconutil -c icns build/darwin/loom.iconset -o build/darwin/loom.icns && rm -rf build/darwin/loom.iconset

internal/gui/tray.png build/darwin/loom.icns:
	@$(MAKE) --no-print-directory icons

build: ui internal/gui/tray.png build/darwin/loom.icns
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o loom .

app: build
	@rm -rf loom.app
	@mkdir -p loom.app/Contents/MacOS loom.app/Contents/Resources
	@cp loom loom.app/Contents/MacOS/loom
	@cp build/darwin/loom.icns loom.app/Contents/Resources/loom.icns
	@sed 's/@VERSION@/$(VERSION)/' build/darwin/Info.plist > loom.app/Contents/Info.plist
	@echo "  loom.app"

test: ui internal/gui/tray.png
	$(GO) vet ./... && $(GO) test ./...

dev: ui internal/gui/tray.png
	@$(GO) build -tags dev -o loom-dev . && ./loom-dev

clean:
	rm -rf loom loom-dev loom.app dist dist-server
