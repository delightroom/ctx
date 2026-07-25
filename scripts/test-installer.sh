#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/ctx-installer-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

case "$(uname -s)" in
  Darwin) target_os="darwin" ;;
  Linux) target_os="linux" ;;
  *) printf '%s\n' "unsupported test operating system" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) target_arch="amd64" ;;
  arm64 | aarch64) target_arch="arm64" ;;
  *) printf '%s\n' "unsupported test architecture" >&2; exit 1 ;;
esac

version="0.1.0"
tag="v${version}"
archive="ctx_${version}_${target_os}_${target_arch}.tar.gz"
release_directory="${test_root}/releases/${tag}"
payload_directory="${test_root}/payload"
install_directory="${test_root}/bin"
home_directory="${test_root}/home"
data_directory="${test_root}/data"

mkdir -p "$release_directory" "$payload_directory" "$home_directory"
printf '#!/bin/sh\nprintf "ctx test release %s\\n"\n' "$version" >"${payload_directory}/ctx"
chmod +x "${payload_directory}/ctx"
tar -czf "${release_directory}/${archive}" -C "$payload_directory" ctx

if command -v sha256sum >/dev/null 2>&1; then
  digest=$(sha256sum "${release_directory}/${archive}" | awk '{print $1}')
else
  digest=$(shasum -a 256 "${release_directory}/${archive}" | awk '{print $1}')
fi
printf '%s  %s\n' "$digest" "$archive" >"${release_directory}/checksums.txt"

HOME="$home_directory" \
XDG_DATA_HOME="$data_directory" \
CTX_RELEASE_ROOT="file://${test_root}/releases" \
  sh "${repository_root}/install.sh" \
  --version "$tag" \
  --install-dir "$install_directory" \
  --no-modify-path >"${test_root}/first-install.log"

installed_output=$("${install_directory}/ctx")
canonical_install_directory=$(cd "$install_directory" && pwd)
[ "$installed_output" = "ctx test release ${version}" ]
[ "$(grep -c "context travels better together" "${test_root}/first-install.log")" -eq 1 ]
grep -F "ctx ${version} · ${target_os}/${target_arch} · standalone" "${test_root}/first-install.log" >/dev/null
grep -F "binary  ${canonical_install_directory}/ctx" "${test_root}/first-install.log" >/dev/null
[ "$(sed -n '1p' "${data_directory}/ctx/install-method")" = "standalone" ]
[ "$(sed -n '2p' "${data_directory}/ctx/install-method")" = "${canonical_install_directory}/ctx" ]

HOME="$home_directory" \
XDG_DATA_HOME="$data_directory" \
CTX_RELEASE_ROOT="file://${test_root}/releases" \
  sh "${repository_root}/install.sh" \
  --version "$tag" \
  --install-dir "$install_directory" \
  --no-modify-path >"${test_root}/repeat-install.log"
if grep -q "context travels better together" "${test_root}/repeat-install.log"; then
  printf '%s\n' "installer replayed first-install art during an update" >&2
  exit 1
fi

printf '%064d  %s\n' 0 "$archive" >"${release_directory}/checksums.txt"
if HOME="$home_directory" \
  XDG_DATA_HOME="$data_directory" \
  CTX_RELEASE_ROOT="file://${test_root}/releases" \
  sh "${repository_root}/install.sh" \
  --version "$tag" \
  --install-dir "${test_root}/bad-bin" \
  --no-modify-path >/dev/null 2>&1; then
  printf '%s\n' "installer accepted a mismatched checksum" >&2
  exit 1
fi

printf '%s\n' "installer integration test passed"
