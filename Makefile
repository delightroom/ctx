DEV_INSTALL_ROOT := $(HOME)/.local/share/ctx/dev
DEV_INSTALL_BIN := $(DEV_INSTALL_ROOT)/bin
DEV_BINARY := $(DEV_INSTALL_BIN)/ctx

.PHONY: build test vet check test-installer test-dev-path install install-dev run-dev uninstall-dev check-dev-path

build:
	go build -o bin/ctx ./cmd/ctx

test:
	go test ./...

vet:
	go vet ./...

test-installer:
	sh -n install.sh scripts/test-installer.sh
	sh scripts/test-installer.sh

test-dev-path:
	@if $(MAKE) --no-print-directory DEV_INSTALL_BIN=/tmp/ctx-unsafe check-dev-path >/dev/null 2>&1; then \
		printf '%s\n' "unsafe DEV_INSTALL_BIN override was accepted"; \
		exit 1; \
	fi
	@if $(MAKE) --no-print-directory DEV_BINARY=/tmp/ctx-unsafe check-dev-path >/dev/null 2>&1; then \
		printf '%s\n' "unsafe DEV_BINARY override was accepted"; \
		exit 1; \
	fi

check: test vet test-installer test-dev-path

check-dev-path:
	@test -n "$(HOME)" || { printf '%s\n' "HOME is not set"; exit 1; }
	@test "$(DEV_INSTALL_ROOT)" = "$(HOME)/.local/share/ctx/dev" || { \
		printf '%s\n' "refusing unsafe DEV_INSTALL_ROOT: $(DEV_INSTALL_ROOT)"; \
		exit 1; \
	}
	@test "$(DEV_INSTALL_BIN)" = "$(HOME)/.local/share/ctx/dev/bin" || { \
		printf '%s\n' "refusing unsafe DEV_INSTALL_BIN: $(DEV_INSTALL_BIN)"; \
		exit 1; \
	}
	@test "$(DEV_BINARY)" = "$(HOME)/.local/share/ctx/dev/bin/ctx" || { \
		printf '%s\n' "refusing unsafe DEV_BINARY: $(DEV_BINARY)"; \
		exit 1; \
	}

install: install-dev

install-dev: check-dev-path
	@mkdir -p "$(DEV_INSTALL_BIN)"
	go build -o "$(DEV_BINARY)" ./cmd/ctx
	@printf 'Installed development ctx to %s\n' "$(DEV_BINARY)"
	@printf 'Run: make run-dev\n'

run-dev: install-dev
	PATH="$(DEV_INSTALL_BIN):$$PATH" ctx

uninstall-dev: check-dev-path
	@rm -f "$(DEV_BINARY)"
	@rmdir "$(DEV_INSTALL_BIN)" "$(DEV_INSTALL_ROOT)" 2>/dev/null || true
	@printf 'Removed development ctx from %s\n' "$(DEV_BINARY)"
