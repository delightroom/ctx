# ctx

`ctx` hosts live Claude Code or Codex context over a private Tailscale tailnet.
It exposes normalized, read-only interaction history rather than native
session files or host-authored executable code.

## Install

The supported entrypoint for macOS and Linux is:

```bash
curl -fsSL https://delightroom.github.io/ctx/install.sh | sh
ctx
```

The installer detects the operating system and architecture, downloads the
matching release, verifies its SHA-256 checksum, and installs `ctx` into
`~/.local/bin`. It does not require `sudo`.

Pin a release or choose an installation directory when needed:

```bash
curl -fsSL https://delightroom.github.io/ctx/install.sh | \
  sh -s -- --version v0.2.0

curl -fsSL https://delightroom.github.io/ctx/install.sh | \
  sh -s -- --install-dir "$HOME/bin"
```

## Start with `ctx`

Run `ctx` without arguments in a terminal to open the context dashboard.
`ctx tui` is the explicit equivalent.

```text
$ ctx

ctx 0.3.0  |  TAILNET dev-laptop  |  AGENTS Claude Code + Codex
╭─ LOCAL SESSIONS  WORKSPACE  2 ─────╮╭─ SHARED CONTEXTS  2 ──────────────╮
│ Claude  payments             14s ago││ review-host/release-review  Codex │
│ Codex   ctx                   2m ago││ api-host/payment-debug      Claude│
╰─────────────────────────────────────╯╰───────────────────────────────────╯
╭─ SELECTION ──────────────────────────────────────────────────────────────╮
│ Provider Claude  Project payments  Updated 14s ago                       │
│ Action: Enter or h to host this session                                 │
╰─────────────────────────────────────────────────────────────────────────╯
tab focus  ↑↓ move  enter actions  / filter  a all  r refresh  ? help  q quit
```

The left panel discovers local Claude Code and Codex sessions. It starts with
the current workspace; press `a` to include every local workspace. The right
panel discovers shared contexts reachable over Tailscale. Both inventories
load independently, so local browsing remains available if the tailnet is
offline or a peer does not respond.

The dashboard shows metadata only—provider, project, locator, revision, and
timestamps. It does not render conversation content. Press `Enter` for the
available actions, or use `h`, `t`, `f`, and `c` to host, tail, follow, or
continue directly. The dashboard exits before handing control to the regular
command, keeping those long-running and interactive flows predictable.

The dashboard requires at least a `60x18` terminal and switches from
side-by-side panels to a stacked layout below 100 columns. Press `?` inside it,
or run `ctx tui --help`, for the complete key reference.

Bare `ctx` opens the dashboard only in an interactive terminal. Scripts and CI
receive ordinary help output and should continue to use explicit commands.

## Commands

The stable command interface is:

```text
ctx tui
ctx host
ctx host ls
ctx ls
ctx tail
ctx continue
ctx doctor
ctx update
ctx completion
```

The Unix manual page is available at [`docs/ctx.1`](docs/ctx.1) and can be
opened from a checkout with `man ./docs/ctx.1`.

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

List every hostable Claude Code and Codex session for the current workspace:

```bash
ctx host ls
```

Sessions are sorted newest first. The listing includes the provider, project,
last-modified time, session ID, and exact source path accepted by
`ctx host --source`.

Scan every workspace in both local session stores when needed:

```bash
ctx host ls --all
ctx host ls --json
```

Discovery reads session metadata only; conversation events are not rendered by
the listing command.

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
