#!/bin/sh
# Runs install.sh against the release files in dist/ (make dist) on this machine: it must install a working zenavo,
# and must refuse a download that does not match SHA256SUMS. CI runs it on Linux and macOS.

set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
[ -f "$root/dist/SHA256SUMS" ] || {
	echo "run make dist first" >&2
	exit 1
}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp -R "$root/dist" "$work/release"

# A good release installs, into a directory that is not on PATH.
out=$(ZENAVO_DOWNLOAD_URL="file://$work/release" ZENAVO_INSTALL_DIR="$work/bin" sh "$root/install.sh")
echo "$out"
"$work/bin/zenavo" version
case $out in *"is not on your PATH"*) ;; *)
	echo "FAIL: no PATH hint" >&2
	exit 1
	;;
esac

# A binary that does not match its checksum is refused and nothing is installed.
case $(uname -s) in Darwin) os=darwin ;; *) os=linux ;; esac
case $(uname -m) in x86_64 | amd64) arch=amd64 ;; *) arch=arm64 ;; esac
printf 'tampered' >>"$work/release/zenavo_${os}_${arch}"
if ZENAVO_DOWNLOAD_URL="file://$work/release" ZENAVO_INSTALL_DIR="$work/bad" sh "$root/install.sh" 2>"$work/err"; then
	echo "FAIL: a tampered binary was installed" >&2
	exit 1
fi
grep -q 'does not match SHA256SUMS' "$work/err" || {
	cat "$work/err" >&2
	exit 1
}
[ ! -e "$work/bad/zenavo" ] || {
	echo "FAIL: something was written for a tampered binary" >&2
	exit 1
}

# A binary missing from SHA256SUMS is refused too.
grep -v "zenavo_${os}_${arch}\$" "$root/dist/SHA256SUMS" >"$work/release/SHA256SUMS"
cp "$root/dist/zenavo_${os}_${arch}" "$work/release/"
if ZENAVO_DOWNLOAD_URL="file://$work/release" ZENAVO_INSTALL_DIR="$work/bad" sh "$root/install.sh" 2>"$work/err"; then
	echo "FAIL: a binary missing from SHA256SUMS was installed" >&2
	exit 1
fi
grep -q 'is not listed in SHA256SUMS' "$work/err" || {
	cat "$work/err" >&2
	exit 1
}
echo "install.sh: ok"
