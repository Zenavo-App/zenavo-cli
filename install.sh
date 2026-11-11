#!/bin/sh
# Installs the Zenavo CLI on macOS and Linux:
#
#   curl -fsSL https://zenavo.app/install.sh | sh
#
# It downloads the binary for this system from a release of github.com/Zenavo-App/zenavo-cli, checks it against
# the release's SHA256SUMS and puts it on your PATH. Nothing runs as root: it installs to /usr/local/bin when you can
# write there, and to ~/.local/bin otherwise. Settings, all optional:
#
#   ZENAVO_VERSION       a release tag such as v0.2.0 (default: the latest release)
#   ZENAVO_INSTALL_DIR   the directory to install zenavo into
#   ZENAVO_DOWNLOAD_URL  where the release files are, for a mirror (default: the GitHub release)
#
# Everything is inside main, which runs on the last line, so a download cut short runs nothing.

set -eu

fail() {
	echo "zenavo install: $*" >&2
	exit 1
}

fetch() { # url file
	if command -v curl >/dev/null 2>&1; then
		# shellcheck disable=SC2086 # $proto is empty or several flags
		curl -fsSL $proto -o "$2" "$1" || fail "could not download $1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1" || fail "could not download $1"
	else
		fail "curl or wget is needed"
	fi
}

sha256() { # file
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/^.*= //'
	else
		fail "sha256sum, shasum or openssl is needed to check the download"
	fi
}

main() {
	case $(uname -s) in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) fail "this script is for macOS and Linux; on Windows run: irm https://zenavo.app/install.ps1 | iex" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "no build for this processor ($(uname -m)); see https://github.com/Zenavo-App/zenavo-cli to build from source" ;;
	esac

	# The default source is GitHub over HTTPS only. A mirror is the user's choice, scheme included.
	proto="--proto =https --tlsv1.2"
	base=${ZENAVO_DOWNLOAD_URL:-}
	if [ -n "$base" ]; then
		proto=
	elif [ -n "${ZENAVO_VERSION:-}" ]; then
		base=https://github.com/Zenavo-App/zenavo-cli/releases/download/$ZENAVO_VERSION
	else
		base=https://github.com/Zenavo-App/zenavo-cli/releases/latest/download
	fi
	base=${base%/}

	file=zenavo_${os}_${arch}
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' INT TERM
	fetch "$base/$file" "$tmp/$file"
	fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"

	want=$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
	[ -n "$want" ] || fail "$file is not listed in SHA256SUMS"
	got=$(sha256 "$tmp/$file")
	[ "$got" = "$want" ] || fail "the download does not match SHA256SUMS (expected $want, got $got); nothing was installed"

	dir=${ZENAVO_INSTALL_DIR:-}
	if [ -z "$dir" ]; then
		if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
			dir=/usr/local/bin
		else
			dir=$HOME/.local/bin
		fi
	fi
	mkdir -p "$dir" || fail "could not create $dir"
	chmod 755 "$tmp/$file"
	mv -f "$tmp/$file" "$dir/zenavo" || fail "could not write $dir/zenavo; set ZENAVO_INSTALL_DIR to a directory you can write"

	echo "Installed $("$dir/zenavo" version) to $dir/zenavo"
	case ":$PATH:" in
	*":$dir:"*) echo "Get started: zenavo auth login" ;;
	*)
		echo "$dir is not on your PATH. Add it, for example in ~/.profile or ~/.zshrc:"
		echo "  export PATH=\"$dir:\$PATH\""
		;;
	esac
}

main "$@"
