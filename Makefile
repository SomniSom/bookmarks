GO ?= go

.PHONY: run build test cover lint fix fmt vuln tidy extensions image xpi xpi-sign

run:
	$(GO) run ./cmd/bookmarks

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags="-s -w" -o bin/bookmarks ./cmd/bookmarks

test:
	$(GO) test -race ./...

cover:
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

fix:
	$(GO) fix ./...
	gofmt -w .

fmt:
	gofmt -l .

vuln:
	govulncheck ./...

tidy:
	$(GO) mod tidy

image:
	docker build -t ghcr.io/somnisom/bookmarks:latest .

extensions:
	@for d in chrome firefox; do \
		mkdir -p extensions/$$d/icons; \
		cp extensions/shared/*.js extensions/shared/*.html extensions/shared/*.css extensions/$$d/; \
		cp extensions/icons/* extensions/$$d/icons/; \
	done
	@echo "готово: extensions/chrome и extensions/firefox"

xpi: extensions
	mkdir -p dist
	rm -f dist/bookmarks.xpi
	cd extensions/firefox && zip -r -X ../../dist/bookmarks.xpi . -x '*.DS_Store'
	@echo "готово: dist/bookmarks.xpi (неподписанный — обычный Firefox не ставит)"
	@echo "для себя: about:debugging → extensions/firefox/manifest.json"
	@echo "постоянно: make xpi-sign"

# Unlisted AMO: подпись без витрины. Нужны WEB_EXT_API_KEY и WEB_EXT_API_SECRET.
xpi-sign: extensions
	bash scripts/sign-xpi.sh
