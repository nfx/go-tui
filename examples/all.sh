#!/bin/sh
# Runs every example in sequence (except browser).
# Usage: ./all.sh [--ci] [name ...]
#   --ci  run with CI=true, so prompts answer their defaults and nothing
#         redraws, as in a CI job
cd "$(dirname "$0")" || exit 1

ci=""
if [ "$1" = "--ci" ]; then
	ci=1
	shift
fi

if [ $# -gt 0 ]; then
	names="$*"
else
	names=$(for d in */; do echo "${d%/}"; done | grep -vx browser)
fi

failed=""
for name in $names; do
	[ -n "$ci" ] || clear
	echo "=== $name ==="
	if [ -n "$ci" ]; then
		CI=true go run "./$name"
	else
		go run "./$name"
	fi
	status=$?
	# 130 = interrupted by ctrl+c (128+SIGINT); stop everything
	if [ "$status" -eq 130 ] || [ "$status" -eq 2 ]; then
		echo "interrupted in $name"
		exit 130
	fi
	[ "$status" -eq 0 ] || failed="$failed $name"
	[ -n "$ci" ] || sleep 2
done

if [ -n "$failed" ]; then
	echo "FAILED:$failed"
	exit 1
fi
