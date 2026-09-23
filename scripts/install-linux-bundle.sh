#!/bin/sh
set -eu

prefix=/usr/local
if [ "$#" -eq 2 ] && [ "$1" = "--prefix" ]; then
    prefix=$2
elif [ "$#" -ne 0 ]; then
    echo "usage: $0 [--prefix /usr/local]" >&2
    exit 2
fi

[ "$(id -u)" -eq 0 ] || { echo "installation requires root" >&2; exit 1; }
case "$prefix" in
    /*) ;;
    *) echo "prefix must be absolute" >&2; exit 1 ;;
esac
[ -d "$prefix" ] || { echo "prefix must already exist as a trusted root-owned directory" >&2; exit 1; }
[ ! -L "$prefix" ] || { echo "prefix must not be a symlink" >&2; exit 1; }
prefix=$(CDPATH= cd -- "$prefix" && pwd -P)
[ "$prefix" != / ] || { echo "refusing filesystem-root prefix" >&2; exit 1; }

check_trusted_dir() {
    path=$1
    [ -d "$path" ] && [ ! -L "$path" ] || return 1
    [ "$(stat -c %u -- "$path")" -eq 0 ] || return 1
    case "$(stat -c %A -- "$path")" in
        ?????w????|????????w?) return 1 ;;
    esac
}

path=$prefix
while :; do
    check_trusted_dir "$path" || { echo "untrusted installation ancestor: $path" >&2; exit 1; }
    [ "$path" = / ] && break
    path=$(dirname -- "$path")
done

bundle_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
(
    cd "$bundle_dir"
    sha256sum -c SHA256SUMS
)

for path in "$prefix/bin" "$prefix/libexec" "$prefix/libexec/tiana" "$prefix/share" "$prefix/share/tiana"; do
    if [ ! -e "$path" ]; then
        install -d -o root -g root -m 0755 "$path"
    fi
    check_trusted_dir "$path" || { echo "untrusted installation directory: $path" >&2; exit 1; }
done

tmp_helper="$prefix/libexec/tiana/.tiana-helper.$$"
tmp_manifest="$prefix/libexec/tiana/.tiana-helper.manifest.json.$$"
tmp_cli="$prefix/bin/.tiana.$$"
tmp_git="$prefix/bin/.git-remote-tiana.$$"
tmp_ca="$prefix/share/tiana/.gateway-wildcard.der.$$"
trap 'rm -f -- "$tmp_helper" "$tmp_manifest" "$tmp_cli" "$tmp_git" "$tmp_ca"' EXIT HUP INT TERM

install -o root -g root -m 0555 "$bundle_dir/libexec/tiana/tiana-helper" "$tmp_helper"
install -o root -g root -m 0444 "$bundle_dir/libexec/tiana/tiana-helper.manifest.json" "$tmp_manifest"
install -o root -g root -m 0555 "$bundle_dir/bin/tiana" "$tmp_cli"
install -o root -g root -m 0555 "$bundle_dir/bin/git-remote-tiana" "$tmp_git"
if [ -f "$bundle_dir/share/tiana/gateway-wildcard.der" ]; then
    install -o root -g root -m 0444 "$bundle_dir/share/tiana/gateway-wildcard.der" "$tmp_ca"
fi

mv -f -- "$tmp_helper" "$prefix/libexec/tiana/tiana-helper"
mv -f -- "$tmp_manifest" "$prefix/libexec/tiana/tiana-helper.manifest.json"
if [ -f "$tmp_ca" ]; then
    mv -f -- "$tmp_ca" "$prefix/share/tiana/gateway-wildcard.der"
fi
mv -f -- "$tmp_cli" "$prefix/bin/tiana"
mv -f -- "$tmp_git" "$prefix/bin/git-remote-tiana"
trap - EXIT HUP INT TERM

"$prefix/bin/tiana" --version
printf 'installed tiana into %s\n' "$prefix"
