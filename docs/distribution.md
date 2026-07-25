# Distribution

The canonical installation flow is:

```bash
curl -fsSL https://delightroom.github.io/ctx/install.sh | sh
```

The public endpoint is deliberately stable. Release storage and source hosting
can change without changing developer onboarding.

## Release contract

GoReleaser publishes these assets for every `vX.Y.Z` tag:

```text
ctx_X.Y.Z_darwin_amd64.tar.gz
ctx_X.Y.Z_darwin_arm64.tar.gz
ctx_X.Y.Z_linux_amd64.tar.gz
ctx_X.Y.Z_linux_arm64.tar.gz
checksums.txt
install.sh
```

Every archive contains one statically linked executable named `ctx`.
`checksums.txt` is generated from the exact published archives.

Release notes use GitHub's native generator. Pull requests labeled
`enhancement`, `bug`, `documentation`, `dependencies`, or `breaking-change`
are grouped into user-facing sections by `.github/release.yml`; unmatched pull
requests remain visible under **Other changes**.

The installer:

1. detects macOS or Linux and `amd64` or `arm64`;
2. resolves the latest release unless `--version` is provided;
3. downloads the matching archive and `checksums.txt`;
4. verifies SHA-256 before extracting anything;
5. atomically installs `ctx`;
6. records `standalone` as the installation method for `ctx update`;
7. shows the string-phone welcome only when no prior installation record
   exists, ending on a static frame with version, platform, standalone
   installation method, and binary path.

Set `CTX_NO_ANIMATION=1` to render the final first-install artwork without
motion. Non-interactive and `TERM=dumb` installs also use the static form.

## Installer hosting

The Pages workflow publishes the repository's `install.sh` verbatim at
`https://delightroom.github.io/ctx/install.sh` over HTTPS without
authentication. It deploys whenever the script or site configuration changes
on `main`.

The script resolves release metadata and artifacts from the public
`delightroom/ctx` GitHub Releases.

Do not publish the first release until both the installer endpoint and the
release assets are publicly reachable from an unauthenticated machine.

## Testing

The installer integration test creates a local fake release, serves it through
the same URL layout, and verifies the installed executable, installation
record, first-install welcome, and update suppression:

```bash
make test-installer
```

Validate the full release configuration before tagging:

```bash
goreleaser check
goreleaser release --snapshot --clean
```
