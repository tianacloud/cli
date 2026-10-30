#!/bin/sh
set -eu

[ "$#" -eq 0 ] || { echo "usage: $0" >&2; exit 2; }

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd -P)

kernel=$(uname -s)
machine=$(uname -m)
case "$kernel:$machine" in
    Linux:x86_64) target_os=linux; target_arch=amd64 ;;
    Linux:aarch64|Linux:arm64) target_os=linux; target_arch=arm64 ;;
    Linux:riscv64) target_os=linux; target_arch=riscv64 ;;
    Darwin:x86_64) target_os=darwin; target_arch=amd64 ;;
    Darwin:arm64) target_os=darwin; target_arch=arm64 ;;
    *) echo "unsupported release host: $kernel/$machine" >&2; exit 1 ;;
esac

version=${TIANA_RELEASE_VERSION:-1.0.7}
insecure_tls=${TIANA_INSECURE_TLS:-false}
mkdir -p "$repo_dir/dist"
output="$repo_dir/dist/tiana-cli-$target_os-$target_arch-$version"

TIANA_RELEASE_VERSION="$version" \
TIANA_INSECURE_TLS="$insecure_tls" \
    "$script_dir/build-single-binary.sh" \
    "$target_os" "$target_arch" \
    "$output"
