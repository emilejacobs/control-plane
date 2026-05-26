.PHONY: test build cross-build ci clean edge-ui build-edge-ui-binary

BIN_DIR := bin
TARGETS := darwin-arm64 darwin-amd64 linux-arm64
EDGE_UI_EMBED_DIR := cmd/uknomi-edge-ui/edge-ui-out

test:
	go test ./...

build:
	go build ./...

cross-build: | $(BIN_DIR)
	@for t in $(TARGETS); do \
		os=$${t%-*}; arch=$${t##*-}; \
		echo "→ $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o $(BIN_DIR)/agent-$$t ./cmd/agent || exit 1; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o $(BIN_DIR)/agent-cli-$$t ./cmd/agent-cli || exit 1; \
	done

# edge-ui builds the Next.js static export under edge-ui/out and
# overlays it onto cmd/uknomi-edge-ui/edge-ui-out so the Go binary's
# //go:embed picks up real assets instead of the gitignored
# placeholder. The .gitignore in the embed dir stays put (cp -R of
# edge-ui/out doesn't touch it). Run this target before
# `go build ./cmd/uknomi-edge-ui` whenever the Next.js sources change.
edge-ui:
	cd edge-ui && npm ci && npm run build
	cp -R edge-ui/out/. $(EDGE_UI_EMBED_DIR)/

# build-edge-ui-binary is the canonical command for producing a
# distributable uknomi-edge-ui — it depends on `edge-ui` so the
# embedded bundle is fresh.
build-edge-ui-binary: edge-ui | $(BIN_DIR)
	go build -o $(BIN_DIR)/uknomi-edge-ui ./cmd/uknomi-edge-ui

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

ci: test cross-build
	@echo "✓ CI checks passed"

clean:
	rm -rf $(BIN_DIR)
	rm -rf edge-ui/out edge-ui/.next
