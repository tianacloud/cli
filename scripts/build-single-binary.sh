#!/bin/sh
set -eu

usage() {
    echo "usage: $0 GOOS GOARCH OUTPUT (Go helper is built in)" >&2
    exit 2
}

[ "$#" -eq 3 ] || usage

target_os=$1
target_arch=$2
output=$3
version=${TIANA_RELEASE_VERSION:-1.0.1}
insecure_tls=${TIANA_INSECURE_TLS:-false}
go_bin=${GO_BIN:-go}

case "$target_os:$target_arch" in
    linux:amd64|linux:arm64|linux:riscv64|darwin:amd64|darwin:arm64) ;;
    *) echo "unsupported release target: $target_os/$target_arch" >&2; exit 1 ;;
esac
case "$version" in
    *[!0-9A-Za-z._+-]*|'') echo "invalid TIANA_RELEASE_VERSION" >&2; exit 1 ;;
esac
case "$insecure_tls" in
    true|false) ;;
    *) echo "TIANA_INSECURE_TLS must be true or false" >&2; exit 1 ;;
esac


script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd -P)
output_parent=$(dirname -- "$output")
case "$(basename -- "$output")" in
    git-remote-tiana|git-remote-tiana.sha256) echo 'output name is reserved for Git launcher' >&2; exit 1 ;;
esac

[ -d "$output_parent" ] || { echo "output parent does not exist: $output_parent" >&2; exit 1; }
[ ! -e "$output" ] || { echo "refusing to overwrite: $output" >&2; exit 1; }
[ ! -e "$output_parent/git-remote-tiana.sha256" ] || { echo "launcher checksum already exists" >&2; exit 1; }
[ ! -e "$output_parent/git-remote-tiana" ] || { echo "launcher already exists" >&2; exit 1; }
[ ! -e "$output.sha256" ] || { echo "refusing to overwrite: $output.sha256" >&2; exit 1; }
output_parent=$(CDPATH='' cd -- "$output_parent" && pwd -P)
output="$output_parent/$(basename -- "$output")"

sha_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

(
    cd "$repo_dir"
    GOWORK=off GOFLAGS= CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" "$go_bin" build \
        -mod=readonly -buildvcs=false -trimpath \
        -ldflags "-s -w -buildid= -X main.version=$version -X github.com/tianacloud/cli/internal/buildconfig.InsecureTLS=$insecure_tls" \
        -o "$output" ./cmd/tiana
)
chmod 0555 "$output"
install -m 0555 "$script_dir/git-remote-tiana" "$output_parent/git-remote-tiana"

output_sha=$(sha_file "$output")
printf '%s  %s\n' "$output_sha" "$(basename -- "$output")" >"$output.sha256"
chmod 0444 "$output.sha256"
launcher_sha=$(sha_file "$output_parent/git-remote-tiana")
printf '%s  git-remote-tiana\n' "$launcher_sha" >"$output_parent/git-remote-tiana.sha256"
chmod 0444 "$output_parent/git-remote-tiana.sha256"

if [ "$target_os:$target_arch" = linux:amd64 ]; then
    "$output" version
fi
printf 'created %s (%s)\n' "$output" "$output_sha"
