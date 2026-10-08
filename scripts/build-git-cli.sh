#!/bin/sh
set -eu
[ "$#" -eq 1 ] || { echo 'usage: scripts/build-git-cli.sh OUTPUT-DIRECTORY' >&2; exit 2; }
repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
output=$1
[ ! -e "$output" ] || { echo 'output directory already exists' >&2; exit 1; }
mkdir -p "$output"
output=$(CDPATH='' cd -- "$output" && pwd -P)
(cd "$repo_dir" && CGO_ENABLED=0 go build -mod=readonly -trimpath -o "$output/tiana" ./cmd/tiana)
install -m 0555 "$repo_dir/scripts/git-remote-tiana" "$output/git-remote-tiana"

(
    cd "$output"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum tiana git-remote-tiana > SHA256SUMS
    else
        shasum -a 256 tiana git-remote-tiana > SHA256SUMS
    fi
)
chmod 0444 "$output/SHA256SUMS"
