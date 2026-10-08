#!/bin/sh
set -eu

prefix=/usr/local
if [ "$#" -eq 2 ] && [ "$1" = "--prefix" ]; then
    prefix=$2
elif [ "$#" -ne 0 ]; then
    echo "usage: $0 [--prefix /usr/local]" >&2
    exit 2
fi

[ "$(id -u)" -eq 0 ] || { echo "uninstallation requires root" >&2; exit 1; }
case "$prefix" in
    /*) ;;
    *) echo "prefix must be absolute" >&2; exit 1 ;;
esac
# A trailing slash makes test -L dereference a directory symlink. Strip only
# those slashes, without resolving any path component.
while [ "$prefix" != / ] && [ "${prefix%/}" != "$prefix" ]; do
    prefix=${prefix%/}
done
[ -d "$prefix" ] || { echo "prefix does not exist" >&2; exit 1; }
[ ! -L "$prefix" ] || { echo "prefix must not be a symlink" >&2; exit 1; }

check_trusted_dir() {
    path=$1
    [ -d "$path" ] && [ ! -L "$path" ] || return 1
    [ "$(stat -c %u -- "$path")" -eq 0 ] || return 1
    case "$(stat -c %A -- "$path")" in
        ?????w????|????????w?) return 1 ;;
    esac
}

# Validate the original path before normalization so an ancestor symlink
# cannot disappear through pwd -P. Root-owned, non-writable parents also
# prevent unprivileged replacement between validation and removal.
path=$prefix
while :; do
    check_trusted_dir "$path" || { echo "untrusted installation ancestor: $path" >&2; exit 1; }
    parent=$(dirname -- "$path")
    [ "$parent" = "$path" ] && break
    path=$parent
done
prefix=$(CDPATH= cd -- "$prefix" && pwd -P)
[ "$prefix" != / ] || { echo "refusing filesystem-root prefix" >&2; exit 1; }

# Optional bundle directories may already be absent. Every existing path
# component, including a dangling symlink, must pass before any rm runs.
for path in "$prefix/bin" "$prefix/libexec" "$prefix/libexec/tiana" "$prefix/share" "$prefix/share/tiana"; do
    if [ -e "$path" ] || [ -L "$path" ]; then
        check_trusted_dir "$path" || { echo "untrusted installation directory: $path" >&2; exit 1; }
    fi
done

for path in \
    "$prefix/bin/tiana" \
    "$prefix/bin/git-remote-tiana" \
    "$prefix/libexec/tiana/tiana-helper" \
    "$prefix/libexec/tiana/tiana-helper.manifest.json" \
    "$prefix/share/tiana/gateway-wildcard.der"
do
    if [ -L "$path" ]; then
        echo "refusing to remove symlink: $path" >&2
        exit 1
    fi
done

rm -f -- \
    "$prefix/bin/tiana" \
    "$prefix/bin/git-remote-tiana" \
    "$prefix/libexec/tiana/tiana-helper" \
    "$prefix/libexec/tiana/tiana-helper.manifest.json" \
    "$prefix/share/tiana/gateway-wildcard.der"
rmdir -- "$prefix/libexec/tiana" 2>/dev/null || true
rmdir -- "$prefix/share/tiana" 2>/dev/null || true
printf 'removed the Tiana CLI files from %s; this operation is not recoverable by the script\n' "$prefix"
