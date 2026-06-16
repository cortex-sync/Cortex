.PHONY: fmt lint validate build build-all clean hooks-install release-dry-run licenses e2e mcpb mcpb-all mcpb-check test-launcher

fmt:
	cd mcp/git-server && make fmt

lint:
	cd mcp/git-server && make lint

validate:
	cd mcp/git-server && make validate

build:
	cd mcp/git-server && make build

build-all:
	cd mcp/git-server && make build-all

clean:
	cd mcp/git-server && make clean

licenses:
	bash scripts/gen-third-party-licenses.sh

# Containerised end-to-end test against a disposable Gitea over HTTPS.
# Requires docker (compose plugin), openssl, curl. See e2e/README.md.
e2e:
	bash e2e/run.sh

# Pack a .mcpb desktop-extension bundle for the host platform (Cowork / Claude
# Desktop local-MCP install). See scripts/pack-mcpb.sh.
mcpb:
	bash scripts/pack-mcpb.sh

# Pack a .mcpb for every released target (matches the goreleaser build matrix:
# linux/darwin amd64+arm64, windows amd64; no windows/arm64).
mcpb-all:
	bash scripts/pack-mcpb.sh linux amd64
	bash scripts/pack-mcpb.sh linux arm64
	bash scripts/pack-mcpb.sh darwin amd64
	bash scripts/pack-mcpb.sh darwin arm64
	bash scripts/pack-mcpb.sh windows amd64

# Verify the structure of every .mcpb in dist/ (manifest, entry_point, exec
# bit, icon, tools). Runs in CI; run locally after `make mcpb`/`mcpb-all`.
mcpb-check:
	python3 scripts/check-mcpb.py dist

# Test the launcher's SHA-256 integrity gate (fail-closed) against a local
# fixture release - no network. See scripts/test-launcher.sh.
test-launcher:
	bash scripts/test-launcher.sh

hooks-install:
	@which lefthook > /dev/null || (echo "Installing lefthook..." && go install github.com/evilmartians/lefthook@latest)
	$(shell go env GOPATH)/bin/lefthook install
	@echo "Pre-commit hooks installed"

release-dry-run:
	cd mcp/git-server && go mod tidy
	goreleaser release --snapshot --clean
