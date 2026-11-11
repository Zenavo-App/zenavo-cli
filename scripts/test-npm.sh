#!/bin/sh
# Packs the npm packages that scripts/npm-packages.mjs makes from dist/ (make dist), installs them into a throwaway
# project without the registry and runs zenavo through npx. Also checks that the launcher explains itself when the
# binary for this machine is missing. CI runs it on Linux and macOS; it needs Node and npm.

set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
[ -f "$root/dist/SHA256SUMS" ] || {
	echo "run make dist first" >&2
	exit 1
}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export npm_config_cache="$work/cache" npm_config_update_notifier=false npm_config_fund=false npm_config_audit=false

node "$root/scripts/npm-packages.mjs" v0.0.0-test "$root/dist" "$work/pkg" >/dev/null
platform=$(node -p 'process.platform + "-" + process.arch')
mkdir "$work/tgz"
(cd "$work/tgz" && npm pack --silent "$work/pkg/zenavo" "$work/pkg/cli-$platform" >/dev/null)

# Every file the packages would publish, and nothing else.
(cd "$work/pkg/zenavo" && npm pack --dry-run --json | node -e 'const f = JSON.parse(require("fs").readFileSync(0))[0].files.map(x => x.path).sort().join(" "); if (f !== "LICENSE README.md bin/zenavo.js package.json") { console.error("zenavo packs: " + f); process.exit(1) }')

# Both packages, offline: the other platforms' optional packages are skipped.
mkdir "$work/app" && cd "$work/app"
npm init -y >/dev/null
npm install --offline --silent "$work/tgz/zenavo-0.0.0-test.tgz" "$work/tgz/zenavo-app-cli-$platform-0.0.0-test.tgz"
out=$(npx --no-install zenavo version)
echo "$out"
case $out in "zenavo "*) ;; *)
	echo "FAIL: zenavo version printed '$out'" >&2
	exit 1
	;;
esac
# The exit code comes through.
if npx --no-install zenavo no-such-command >/dev/null 2>&1; then
	echo "FAIL: a failing command exited 0" >&2
	exit 1
fi

# Without the platform package (zenavo alone, offline), the launcher says what is wrong.
mkdir "$work/bare" && cd "$work/bare"
npm init -y >/dev/null
npm install --offline --silent "$work/tgz/zenavo-0.0.0-test.tgz"
[ ! -e "node_modules/@zenavo-app/cli-$platform" ] || {
	echo "FAIL: the platform package came from somewhere" >&2
	exit 1
}
if npx --no-install zenavo version 2>"$work/err"; then
	echo "FAIL: ran without its binary" >&2
	exit 1
fi
grep -q "is not installed" "$work/err" || {
	cat "$work/err" >&2
	exit 1
}
echo "npm packages: ok"
