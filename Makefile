.PHONY: test build cross-build ci clean

BIN_DIR := bin
TARGETS := darwin-arm64 darwin-amd64 linux-arm64

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

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

ci: test cross-build
	@echo "✓ CI checks passed"

clean:
	rm -rf $(BIN_DIR)
