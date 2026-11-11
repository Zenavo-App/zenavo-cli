#!/bin/sh
# Stages the npm packages that scripts/npm-packages.mjs made in <out> (npm stage publish, npm 12 or later), the
# platform packages first so that zenavo never points at a version that is not there yet. Staged packages are not
# public until a maintainer approves them with 2FA, in the same order: npm stage list, then npm stage approve <id>.
# A version already published is skipped. One already staged makes npm refuse it again: approve or reject it first.
# The release workflow runs this with provenance; CI runs it with --dry-run before a release depends on it.
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
npm stage --help >/dev/null 2>&1 || {
	echo "this npm ($(npm -v)) cannot stage packages; npm 12 or later is needed" >&2
	exit 1
}

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
	npm stage publish "$abs" $flags --access public --tag "$tag"
	n=$((n + 1))
done
if [ "$n" -gt 0 ] && [ "$mode" != --dry-run ]; then
	echo "Staged $n package(s). Approve the @zenavo-app/cli-* ones first, then zenavo: npm stage list, npm stage approve <id>."
fi
