BINARY      := claude-status-go
INSTALL_DIR := $(HOME)/.claude
SETTINGS    := $(INSTALL_DIR)/settings.json

# Customization (optional): make install THEME=dracula BAR=block
THEME       ?=
BAR         ?=

VERSION     := $(shell git describe --tags --always 2>/dev/null || echo "dev")
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME  := $(shell date +%s)
LDFLAGS_PKG := github.com/xgarcia/claude-status-go/pkg
LDFLAGS     := -s -w \
               -X $(LDFLAGS_PKG).BuildVersion=$(VERSION) \
               -X $(LDFLAGS_PKG).BuildCommit=$(COMMIT) \
               -X $(LDFLAGS_PKG).BuildTime=$(BUILD_TIME)

.PHONY: all build install uninstall clean test release-snapshot

all: build install

build:
	go build -ldflags='$(LDFLAGS)' -o $(BINARY) .

test:
	go test -v ./...

release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest --snapshot --skip=publish --clean

install: build
	@mkdir -p $(INSTALL_DIR)
	@if [ -f "$(INSTALL_DIR)/$(BINARY)" ]; then \
		cp "$(INSTALL_DIR)/$(BINARY)" "$(INSTALL_DIR)/$(BINARY).bak"; \
		echo "✓ Backed up existing binary to $(BINARY).bak"; \
	fi
	@cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@chmod 755 $(INSTALL_DIR)/$(BINARY)
	@echo "✓ Installed binary to $(INSTALL_DIR)/$(BINARY)"
	@# Build command with optional flags
	@CMD="$$HOME/.claude/$(BINARY)"; \
	if [ -n "$(THEME)" ]; then CMD="$$CMD --theme $(THEME)"; fi; \
	if [ -n "$(BAR)" ]; then CMD="$$CMD --bar $(BAR)"; fi; \
	if [ -f "$(SETTINGS)" ]; then \
		tmp=$$(mktemp); \
		jq --arg cmd "$$CMD" '.statusLine = {"type": "command", "command": $$cmd}' "$(SETTINGS)" > "$$tmp" && mv "$$tmp" "$(SETTINGS)"; \
	else \
		jq -n --arg cmd "$$CMD" '{"statusLine":{"type":"command","command":$$cmd}}' > "$(SETTINGS)"; \
	fi
	@echo "✓ Updated settings.json with statusLine config"
	@if [ -n "$(THEME)" ]; then echo "  Theme: $(THEME)"; fi
	@if [ -n "$(BAR)" ]; then echo "  Bar:   $(BAR)"; fi
	@echo ""
	@echo "Done! Restart Claude Code to see your new status line."

uninstall:
	@if [ -f "$(INSTALL_DIR)/$(BINARY).bak" ]; then \
		mv "$(INSTALL_DIR)/$(BINARY).bak" "$(INSTALL_DIR)/$(BINARY)"; \
		echo "✓ Restored previous binary from $(BINARY).bak"; \
	elif [ -f "$(INSTALL_DIR)/$(BINARY)" ]; then \
		rm "$(INSTALL_DIR)/$(BINARY)"; \
		echo "✓ Removed $(BINARY)"; \
	fi
	@if [ -f "$(SETTINGS)" ]; then \
		tmp=$$(mktemp); \
		jq 'del(.statusLine)' "$(SETTINGS)" > "$$tmp" && mv "$$tmp" "$(SETTINGS)"; \
		echo "✓ Removed statusLine from settings.json"; \
	fi
	@echo ""
	@echo "Done! Restart Claude Code to apply changes."

clean:
	rm -f $(BINARY)
