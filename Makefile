.PHONY: build test install clean vendor-js

TURNDOWN_VERSION := 7.2.2
GFM_VERSION := 1.0.2
JS_DIR := internal/browser/js/vendor

build:
	go build -o bin/af ./cmd/af
	go install ./cmd/af

test: build
	go test -v -count=1 ./...

install:
	go install ./cmd/af

clean:
	rm -rf bin/

vendor-js:
	mkdir -p $(JS_DIR)
	curl -sL https://unpkg.com/turndown@$(TURNDOWN_VERSION)/dist/turndown.js -o $(JS_DIR)/turndown.js
	curl -sL https://unpkg.com/turndown-plugin-gfm@$(GFM_VERSION)/dist/turndown-plugin-gfm.js -o $(JS_DIR)/turndown-plugin-gfm.js
	@echo "Downloaded turndown $(TURNDOWN_VERSION) + GFM plugin $(GFM_VERSION)"
