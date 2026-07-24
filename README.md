# ctx

`ctx` hosts live Claude Code or Codex context over a private Tailscale tailnet.
It exposes normalized, read-only interaction history rather than native
session files or host-authored executable code.

## Install

The supported entrypoint for macOS and Linux is:

```bash
curl -fsSL https://ctx.droom.dev/install.sh | sh
ctx
```

The installer detects the operating system and architecture, downloads the
matching release, verifies its SHA-256 checksum, and installs `ctx` into
`~/.local/bin`. It does not require `sudo`.

Pin a release or choose an installation directory when needed:

```bash
curl -fsSL https://ctx.droom.dev/install.sh | \
  sh -s -- --version v0.1.0

curl -fsSL https://ctx.droom.dev/install.sh | \
  sh -s -- --install-dir "$HOME/bin"
```

## Start with `ctx`

Run `ctx` without arguments to open the interactive launcher:

```text
$ ctx

ctx 0.1.0

✓ Tailscale connected as dev-laptop
✓ Claude Code and Codex detected
– Discovering shared contexts...

Available contexts

  1. review-host/release-review       Codex    14s ago
  2. api-host/payment-debug           Claude    2m ago

What do you want to do?
  1. Continue a context
  2. Tail a context
  3. Host this session
  4. Diagnose setup
```

The launcher appears only in an interactive terminal. Scripts and CI receive
ordinary help output and should use explicit commands.

## Commands

The stable command interface is:

```text
ctx host
ctx ls
ctx tail
ctx continue
ctx doctor
ctx update
ctx completion
```

### Host

Run from the project whose current session should be shared:

```bash
ctx host
ctx host --name payments-debug
```

`ctx` discovers the newest Claude or Codex session associated with the current
working directory, starts a loopback HTTP server, and runs Tailscale Serve on
HTTPS port `8443`. The command stays in the foreground; `Ctrl-C` stops both the
backend and the Tailscale proxy.

Use an explicit session when discovery is ambiguous:

```bash
ctx host --source ~/.claude/projects/.../session.jsonl
```

For local development without Tailscale:

```bash
ctx host --no-tailscale --source testdata/claude.jsonl
```

The command prints a portable locator such as:

```text
ctx://dev-laptop/payments-debug
```

### List

List feeds from a known host:

```bash
ctx ls dev-laptop
```

With no host, `ctx ls` probes online Tailscale peers on the ctx Serve port:

```bash
ctx ls
```

Use `ctx ls --json` for machine-readable output.

### Tail

Print the latest neutral context:

```bash
ctx tail ctx://dev-laptop/payments-debug
```

Follow new revisions using ETag polling:

```bash
ctx tail -f dev-laptop/payments-debug
```

Remote content is printed with `Q>` quarantine markers.

In an interactive terminal, `ctx tail` without a locator opens a feed selector.

### Continue

Start a new interactive agent with a pinned neutral digest:

```bash
ctx continue ctx://dev-laptop/payments-debug --with claude
ctx continue ctx://dev-laptop/payments-debug --with codex
```

To inspect the exact prompt without launching an agent:

```bash
ctx continue dev-laptop/payments-debug --print
```

`continue` means continue the work, not import the native session. It does not
write to `~/.claude/projects`, `~/.codex/sessions`, or Codex SQLite state.

In an interactive terminal, `ctx continue` without a locator opens a feed
selector.

### Diagnose and update

Inspect Tailscale connectivity, session discovery, installed agents, and the
installation method:

```bash
ctx doctor
ctx doctor --json
```

Update using the same installation method:

```bash
ctx update
```

`ctx` delegates Homebrew-managed installations back to Homebrew and updates
standalone installations through the canonical installer.

Generate basic shell completion:

```bash
ctx completion zsh
ctx completion bash
ctx completion fish
```

## Protocol

The v1 service exposes:

```text
GET /v1/feeds
GET /v1/feeds/{feed}/manifest
GET /v1/feeds/{feed}/digest
```

See [docs/protocol.md](docs/protocol.md) and
[docs/security.md](docs/security.md).

## Why

Agent sessions contain useful intent, decisions, constraints, tool history,
and unfinished work. Existing sharing approaches commonly copy private session
formats or ask the consumer to run a script supplied by the host.

`ctx` uses a smaller contract:

- the producer runs a packaged local binary;
- Tailscale Serve supplies HTTPS, identity, and tailnet policy;
- consumers make ordinary read-only HTTP requests;
- `continue` starts a new session from a neutral, quarantined digest;
- no native Claude/Codex session store is modified.

## Development

Requirements:

- Go 1.26 or newer;
- Tailscale with MagicDNS and HTTPS enabled;
- Claude Code or Codex only when using `ctx continue`.

```bash
make build
make check
go test -race ./...
```

To install a development build from a checkout:

```bash
make install
```

Release tags are built for macOS and Linux on amd64 and arm64 using GoReleaser.
See [docs/distribution.md](docs/distribution.md) for the installer and publishing
contract.
