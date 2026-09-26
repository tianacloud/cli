#!/bin/sh
# Optional integration test: source and lockfile are read-only; builds/tests
# live under this CLI checkout. No Rust is needed to build or run tiana.
set -eu
[ "$#" -eq 1 ] || { echo "usage: sh scripts/test-sqlite-app.sh APP_SQLITE_SOURCE" >&2; exit 2; }
app_source=$(CDPATH='' cd -- "$1" && pwd -P)
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd -P)
build_dir="$repo_dir/.cache/app-target"
mkdir -p "$build_dir" "$repo_dir/.cache/tmp"
cargo build --manifest-path "$app_source/Cargo.toml" --locked --lib --target-dir "$build_dir"
set -- "$build_dir"/debug/deps/libtokio-*.rlib
[ "$#" -eq 1 ] || { echo "use a fresh build directory: multiple Tokio artifacts" >&2; exit 1; }
rustc --edition=2024 "$repo_dir/tests/app_peer.rs" \
    -L "dependency=$build_dir/debug/deps" \
    --extern "app_sqlite=$build_dir/debug/libapp_sqlite.rlib" \
    --extern "tokio=$1" -o "$build_dir/app-peer"
cd "$repo_dir"
TIANA_SQLITE_APP_PEER_BINARY="$build_dir/app-peer" TMPDIR="$repo_dir/.cache/tmp" \
    go test -mod=readonly -race -count=1 -run TestRealApp ./internal/sqlitecli
