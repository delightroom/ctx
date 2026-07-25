#!/bin/sh

set -eu

REPOSITORY="delightroom/ctx"
RELEASE_ROOT="${CTX_RELEASE_ROOT:-https://github.com/${REPOSITORY}/releases/download}"
GITHUB_API="${CTX_GITHUB_API:-https://api.github.com/repos/${REPOSITORY}}"
VERSION="${CTX_VERSION:-latest}"
INSTALL_DIR="${CTX_INSTALL_DIR:-${HOME:-}/.local/bin}"
MODIFY_PATH=1

usage() {
  printf '%s\n' "Install ctx, the tailnet-native AI context sharing CLI."
  printf '\n'
  printf '%s\n' "Usage: install.sh [options]"
  printf '\n'
  printf '%s\n' "Options:"
  printf '%s\n' "  --version VERSION      Install a specific version (for example v0.1.0)"
  printf '%s\n' "  --install-dir PATH     Install into PATH (default: ~/.local/bin)"
  printf '%s\n' "  --no-modify-path       Do not update the shell startup file"
  printf '%s\n' "  -h, --help             Show this help"
}

fail() {
  printf 'ctx installer: %s\n' "$*" >&2
  exit 1
}

print_ctx_art() {
  dog_says=$1
  cat_says=$2
  signal=$3
  printf '       %-9s                    %9s\n' "$dog_says" "$cat_says"
  printf '%s\n' '   / \__                            /\_/\'
  printf '%s\n' '  (    @\___                       ( o.o )'
  printf '  /         O=[_]%s[_]=< > ^ <\n' "$signal"
  printf '%s\n' ' /   (_____/                       /   \'
  printf '%s\n' '/_____/   U                       (_____)'
  printf '\n'
  printf '%s\n' '             ctx  context travels better together'
}

show_first_install_art() {
  installed_version=$1
  installed_platform=$2
  installed_path=$3
  if [ -t 1 ] &&
    [ "${TERM:-dumb}" != "dumb" ] &&
    [ -z "${CTX_NO_ANIMATION:-}" ] &&
    sleep 0.01 2>/dev/null; then
    print_ctx_art "woof?" "..." "o---------------"
    sleep 0.09 2>/dev/null || true
    printf '\033[8A'
    print_ctx_art "woof!" "..." "----o-----------"
    sleep 0.09 2>/dev/null || true
    printf '\033[8A'
    print_ctx_art "woof!" "..." "--------o-------"
    sleep 0.09 2>/dev/null || true
    printf '\033[8A'
    print_ctx_art "woof!" "meow!" "------------o---"
    sleep 0.09 2>/dev/null || true
    printf '\033[8A'
  fi
  print_ctx_art "connected" "connected" "-------o--------"
  printf '             ctx %s · %s · standalone\n' "$installed_version" "$installed_platform"
  printf '             binary  %s\n' "$installed_path"
}

download() {
  source_url=$1
  destination=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --connect-timeout 10 "$source_url" -o "$destination"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -q "$source_url" -O "$destination"
    return
  fi
  fail "curl or wget is required"
}

latest_version() {
  metadata_file=$1
  download "${GITHUB_API}/releases/latest" "$metadata_file"
  tag=$(sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' "$metadata_file" | head -n 1)
  [ -n "$tag" ] || fail "could not determine the latest ctx version"
  printf '%s\n' "$tag"
}

sha256() {
  target=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$target" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$target" | awk '{print $1}'
    return
  fi
  fail "sha256sum or shasum is required to verify the download"
}

append_path() {
  directory=$1
  case ":${PATH:-}:" in
    *":${directory}:"*) return ;;
  esac

  if [ "$MODIFY_PATH" -eq 0 ]; then
    printf '%s\n' "– ${directory} is not currently on PATH"
    printf '  Add it with: export PATH="%s:$PATH"\n' "$directory"
    return
  fi

  shell_name=$(basename "${SHELL:-}")
  case "$shell_name" in
    zsh)
      startup_file="${ZDOTDIR:-${HOME}}/.zshrc"
      path_line="export PATH=\"${directory}:\$PATH\""
      ;;
    bash)
      startup_file="${HOME}/.bashrc"
      path_line="export PATH=\"${directory}:\$PATH\""
      ;;
    fish)
      startup_file="${XDG_CONFIG_HOME:-${HOME}/.config}/fish/conf.d/ctx.fish"
      path_line="fish_add_path \"${directory}\""
      ;;
    *)
      printf '%s\n' "– ${directory} is not currently on PATH"
      printf '  Add it with: export PATH="%s:$PATH"\n' "$directory"
      return
      ;;
  esac

  startup_directory=$(dirname "$startup_file")
  mkdir -p "$startup_directory"
  if [ -f "$startup_file" ] && grep -F -e "$directory" "$startup_file" >/dev/null 2>&1; then
    return
  fi
  {
    printf '\n%s\n' "# Added by the ctx installer"
    printf '%s\n' "$path_line"
  } >>"$startup_file"
  printf '✓ Added %s to PATH in %s\n' "$directory" "$startup_file"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || fail "--version requires a value"
      VERSION=$2
      shift 2
      ;;
    --install-dir)
      [ "$#" -ge 2 ] || fail "--install-dir requires a value"
      INSTALL_DIR=$2
      shift 2
      ;;
    --no-modify-path)
      MODIFY_PATH=0
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

[ -n "${HOME:-}" ] || fail "HOME is not set"
[ -n "$INSTALL_DIR" ] || fail "install directory is empty"

case "$(uname -s)" in
  Darwin) target_os="darwin" ;;
  Linux) target_os="linux" ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) target_arch="amd64" ;;
  arm64 | aarch64) target_arch="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/ctx-install.XXXXXX")
trap 'rm -rf "$temporary_root"' EXIT HUP INT TERM

if [ "$VERSION" = "latest" ]; then
  VERSION=$(latest_version "${temporary_root}/latest.json")
fi

case "$VERSION" in
  v*) release_tag=$VERSION ;;
  *) release_tag="v${VERSION}" ;;
esac
release_version=${release_tag#v}

archive="ctx_${release_version}_${target_os}_${target_arch}.tar.gz"
archive_url="${RELEASE_ROOT}/${release_tag}/${archive}"
checksums_url="${RELEASE_ROOT}/${release_tag}/checksums.txt"

printf 'Installing ctx %s for %s/%s\n\n' "$release_version" "$target_os" "$target_arch"
download "$archive_url" "${temporary_root}/${archive}"
download "$checksums_url" "${temporary_root}/checksums.txt"

expected=$(awk -v archive="$archive" '$2 == archive || $2 == "*" archive {print $1}' \
  "${temporary_root}/checksums.txt")
[ -n "$expected" ] || fail "checksums.txt does not contain ${archive}"
actual=$(sha256 "${temporary_root}/${archive}")
[ "$actual" = "$expected" ] || fail "checksum mismatch for ${archive}"
printf '%s\n' "✓ Verified SHA-256 checksum"

tar -xzf "${temporary_root}/${archive}" -C "$temporary_root"
[ -f "${temporary_root}/ctx" ] || fail "release archive does not contain ctx"

mkdir -p "$INSTALL_DIR"
canonical_install_dir=$(cd "$INSTALL_DIR" && pwd)
install_path="${canonical_install_dir}/ctx"
temporary_install="${canonical_install_dir}/.ctx.new.$$"
install -m 0755 "${temporary_root}/ctx" "$temporary_install"
mv -f "$temporary_install" "$install_path"
printf '✓ Installed to %s\n' "$install_path"

data_home="${XDG_DATA_HOME:-${HOME}/.local/share}"
marker_directory="${data_home}/ctx"
mkdir -p "$marker_directory"
first_install=1
if [ -f "${marker_directory}/install-method" ]; then
  first_install=0
fi
printf 'standalone\n%s\n' "$install_path" >"${marker_directory}/install-method"

append_path "$canonical_install_dir"

if [ "$first_install" -eq 1 ]; then
  printf '\n'
  show_first_install_art "$release_version" "${target_os}/${target_arch}" "$install_path"
fi
printf '\n%s\n' "ctx is ready."
case ":${PATH:-}:" in
  *":${canonical_install_dir}:"*) printf '%s\n' "Run: ctx" ;;
  *) printf 'Run now: %s\n' "$install_path" ;;
esac
