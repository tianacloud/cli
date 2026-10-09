#!/bin/sh
set -eu

usage() {
    echo "usage: $0 OUTPUT.tar.gz" >&2
    exit 2
}

[ "$#" -eq 1 ] || usage

case "$(uname -s):$(uname -m)" in
    Linux:x86_64) ;;
    *) echo "tiana bundle build supports Linux amd64 only" >&2; exit 1 ;;
esac

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd -P)
sdk_rust_dir=${TIANA_SDK_RUST_DIR:-"$repo_dir/../sdk"}
version=${TIANA_RELEASE_VERSION:-1.0.1}
insecure_tls=${TIANA_INSECURE_TLS:-false}
go_bin=${GO_BIN:-go}
cargo_bin=${CARGO_BIN:-cargo}
output=$1

case "$version" in
    *[!0-9A-Za-z._+-]*|'') echo "invalid TIANA_RELEASE_VERSION" >&2; exit 1 ;;
esac
case "$insecure_tls" in
    true|false) ;;
    *) echo "TIANA_INSECURE_TLS must be true or false" >&2; exit 1 ;;
esac


output_parent=$(dirname -- "$output")
[ -d "$output_parent" ] || { echo "output parent does not exist: $output_parent" >&2; exit 1; }
[ ! -e "$output" ] || { echo "refusing to overwrite: $output" >&2; exit 1; }
[ ! -e "$output.sha256" ] || { echo "refusing to overwrite: $output.sha256" >&2; exit 1; }
[ -f "$sdk_rust_dir/Cargo.toml" ] || { echo "sdk not found: $sdk_rust_dir" >&2; exit 1; }

stage_dir=$(mktemp -d "${TMPDIR:-/tmp}/tiana-cli-bundle.XXXXXX")
trap 'rm -rf -- "$stage_dir"' EXIT HUP INT TERM
bundle_name="tiana-cli-linux-amd64-$version"
bundle_dir="$stage_dir/$bundle_name"
mkdir -p "$bundle_dir/bin" "$bundle_dir/libexec/tiana" "$bundle_dir/share/tiana"

GOFLAGS= CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build \
    -mod=readonly -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.version=$version -X github.com/tianacloud/cli/internal/buildconfig.InsecureTLS=$insecure_tls" \
    -o "$bundle_dir/bin/tiana" ./cmd/tiana

if [ -n "${TIANA_HELPER_BIN:-}" ]; then
    [ -f "$TIANA_HELPER_BIN" ] || { echo "TIANA_HELPER_BIN is not a regular file" >&2; exit 1; }
    install -m 0555 "$TIANA_HELPER_BIN" "$bundle_dir/libexec/tiana/tiana-helper"
else
    "$cargo_bin" build --locked --release --bin tiana-helper \
        --manifest-path "$sdk_rust_dir/Cargo.toml" \
        --target-dir "$stage_dir/rust-target"
    install -m 0555 "$stage_dir/rust-target/release/tiana-helper" "$bundle_dir/libexec/tiana/tiana-helper"
fi

install -m 0555 "$script_dir/git-remote-tiana" "$bundle_dir/bin/git-remote-tiana"

helper_sha=$(sha256sum "$bundle_dir/libexec/tiana/tiana-helper" | awk '{print $1}')
printf '%s\n' \
    "{\"contract_version\":3,\"helper_relative_path\":\"libexec/tiana/tiana-helper\",\"sha256\":\"$helper_sha\",\"platform\":\"linux\",\"arch\":\"amd64\"}" \
    >"$bundle_dir/libexec/tiana/tiana-helper.manifest.json"
chmod 0444 "$bundle_dir/libexec/tiana/tiana-helper.manifest.json"


printf '%s\n' "$version" >"$bundle_dir/VERSION"
chmod 0444 "$bundle_dir/VERSION"
install -m 0555 "$script_dir/install-linux-bundle.sh" "$bundle_dir/install.sh"
install -m 0555 "$script_dir/uninstall-linux-bundle.sh" "$bundle_dir/uninstall.sh"

(
    cd "$bundle_dir"
    find bin libexec share -type f -print | LC_ALL=C sort | xargs sha256sum >SHA256SUMS
)
chmod 0444 "$bundle_dir/SHA256SUMS"

source_date_epoch=${SOURCE_DATE_EPOCH:-0}
tar --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 --numeric-owner \
    -czf "$output" -C "$stage_dir" "$bundle_name"
output_name=$(basename -- "$output")
(
    cd "$output_parent"
    sha256sum "$output_name" >"$output_name.sha256"
)
chmod 0444 "$output.sha256"
printf 'created %s\n' "$output"
