GO ?= $(HOME)/.local/go/bin/go
export PATH := $(HOME)/.local/go/bin:$(PATH)

.PHONY: run build test extensions

run:
	$(GO) run ./cmd/bookmarks

build:
	mkdir -p bin
	$(GO) build -o bin/bookmarks ./cmd/bookmarks

extensions:
	@for d in chrome firefox; do \
		mkdir -p extensions/$$d/icons; \
		cp extensions/shared/*.js extensions/shared/*.html extensions/shared/*.css extensions/$$d/; \
		cp extensions/icons/* extensions/$$d/icons/; \
	done
	@echo "готово: extensions/chrome и extensions/firefox"
