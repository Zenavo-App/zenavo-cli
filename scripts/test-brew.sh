#!/bin/sh
# Installs the formula that scripts/brew-formula.sh makes for the binaries in dist/ (make dist) from a throwaway
# local tap, runs its test and removes it all again. It checks that the formula installs a working zenavo on this
# machine, before a release depends on it. CI runs it on macOS; it needs Homebrew.

set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
[ -f "$root/dist/SHA256SUMS" ] || {
	echo "run make dist first" >&2
	exit 1
}
version=$("$root/dist/zenavo_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" version | cut -d ' ' -f 2)
case $version in v[0-9]*) ;; *)
	echo "dist was built without a version tag (got $version); run make dist VERSION=v0.0.0-test" >&2
	exit 1
	;;
esac

tap=zenavo-local/test$$
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_FROM_API=1 HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_ENV_HINTS=1
brew tap-new --no-git "$tap" >/dev/null
cleanup() {
	brew uninstall --force "$tap/zenavo" >/dev/null 2>&1 || true
	brew untap --force "$tap" >/dev/null 2>&1 || true
}
trap cleanup EXIT

dir=$(brew --repository "$tap")
"$root/scripts/brew-formula.sh" "$version" "$root/dist/SHA256SUMS" "file://$root/dist" >"$dir/Formula/zenavo.rb"
brew install --formula "$tap/zenavo"
brew test "$tap/zenavo"
"$(brew --prefix)/bin/zenavo" version
echo "brew formula: ok"
