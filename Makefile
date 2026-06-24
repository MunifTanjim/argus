VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
BIN     := bin
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
APP_DIR ?= app

.PHONY: build argus test vet fmt fmt-check tidy check clean install uninstall help \
	app-get app-analyze app-test app-fmt app-check app-run app-build app-clean

build: argus ## Build the binary into bin/

argus: ## Build the unified argus binary (TUI, serve, hooks)
	go build $(LDFLAGS) -o $(BIN)/argus ./cmd/argus

test: ## Run all tests
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format the tree
	gofmt -w .

fmt-check: ## Fail if any file is not gofmt-clean
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

tidy: ## Tidy go.mod/go.sum
	go mod tidy

check: fmt-check vet test ## Run fmt-check, vet, and tests

install: build ## Install the binary into BINDIR (default ~/.local/bin)
	install -d $(BINDIR)
	install -m 0755 $(BIN)/argus $(BINDIR)/argus

uninstall: ## Remove the installed binary from BINDIR
	rm -f $(BINDIR)/argus

clean: ## Remove build artifacts
	rm -rf $(BIN)

# --- Flutter app (app/) ---

app-get: ## Fetch the Flutter app's dependencies
	cd $(APP_DIR) && flutter pub get

app-analyze: ## Run the Dart/Flutter analyzer on the app
	cd $(APP_DIR) && flutter analyze

app-test: ## Run the Flutter app's tests
	cd $(APP_DIR) && flutter test

app-fmt: ## Format the Flutter app's Dart sources
	cd $(APP_DIR) && dart format .

app-check: app-analyze app-test ## Run the app analyzer and tests

app-run: ## Run the Flutter app; fzf-select the device when several are connected (ARGS=... forwarded)
	@cd $(APP_DIR) && \
	lines=$$(flutter devices 2>/dev/null | grep ' • '); \
	line=$$(echo "$$lines" | fzf --prompt='device> ' --select-1 --reverse) || exit 1; \
	device=$$(echo "$$line" | awk -F' • ' '{print $$2}' | xargs); \
	echo "Running on $$device"; \
	flutter run -d "$$device" $(ARGS); \

app-build: ## Build the Flutter app release APK
	cd $(APP_DIR) && flutter build apk

app-clean: ## Remove the Flutter app's build artifacts
	cd $(APP_DIR) && flutter clean

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
