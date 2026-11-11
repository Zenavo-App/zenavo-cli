#!/bin/sh
# Publishes the npm packages that scripts/npm-packages.mjs made in <out>, the platform packages first so that zenavo
# never points at a version that is not there yet. A version already on npm is skipped, so a failed run can simply be
# repeated. The release workflow runs it with provenance; CI runs it with --dry-run to check it before a release
# depends on it.
#
#   scripts/npm-publish.sh <out> <npm-tag> [--dry-run]

set -eu

[ $# -ge 2 ] || {
	echo "usage: $0 <out> <npm-tag> [--dry-run]" >&2
	exit 2
}
out=${1%/}
tag=$2
mode=${3:-}
case $mode in
--dry-run) flags=--dry-run ;;
'') flags=--provenance ;;
*)
	echo "unknown option: $mode" >&2
	exit 2
	;;
esac

n=0
for dir in "$out"/cli-* "$out"/zenavo; do
	[ -f "$dir/package.json" ] || {
		echo "no package in $dir" >&2
		exit 1
	}
	# An absolute path: npm reads "out/zenavo" as the GitHub repository out/zenavo, not as a folder.
	abs=$(cd "$dir" && pwd)
	name=$(node -p "require('$abs/package.json').name")
	version=$(node -p "require('$abs/package.json').version")
	if [ "$mode" != --dry-run ] && npm view "$name@$version" version >/dev/null 2>&1; then
		echo "$name@$version is already published"
		continue
	fi
	npm publish "$abs" $flags --access public --tag "$tag"
	n=$((n + 1))
done
[ "$n" -gt 0 ] || echo "nothing to publish"
