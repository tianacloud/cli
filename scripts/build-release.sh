#!/bin/sh
set -eu

[ "$#" -eq 0 ] || { echo "usage: $0" >&2; exit 2; }

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd -P)
sdk_dir=$(CDPATH='' cd -- "$repo_dir/../sdk" && pwd -P)

kernel=$(uname -s)
machine=$(uname -m)
case "$kernel:$machine" in
    Linux:x86_64)
        target_os=linux
        target_arch=amd64
        rust_target=x86_64-unknown-linux-musl
        musl_cc=$(command -v x86_64-linux-musl-gcc || command -v musl-gcc || true)
        if [ -z "$musl_cc" ]; then
            echo "missing Linux musl C compiler; install musl-tools" >&2
            exit 1
        fi
        export CC_x86_64_unknown_linux_musl="$musl_cc"
        ;;
    Darwin:arm64)
        target_os=darwin
        target_arch=arm64
        rust_target=aarch64-apple-darwin
        ;;
    *)
        echo "unsupported release host: $kernel/$machine" >&2
        exit 1
        ;;
esac

rustup target add "$rust_target"
cargo build \
    --manifest-path "$sdk_dir/Cargo.toml" \
    --locked \
    --release \
    --bin tiana-helper \
    --target "$rust_target"

version=${TIANA_RELEASE_VERSION:-1.0.1}
insecure_tls=${TIANA_INSECURE_TLS:-false}
mkdir -p "$repo_dir/dist"
output="$repo_dir/dist/tiana-cli-$target_os-$target_arch-$version"

TIANA_RELEASE_VERSION="$version" \
TIANA_INSECURE_TLS="$insecure_tls" \
    "$script_dir/build-single-binary.sh" \
    "$target_os" "$target_arch" \
    "$sdk_dir/target/$rust_target/release/tiana-helper" \
    "$output"
