#!/bin/sh
# Runs every example in sequence (except browser).
# Usage: ./all.sh [--no-tty] [name ...]
#   --no-tty  run with stdin from /dev/null and output captured, so the
#             examples see no terminal
cd "$(dirname "$0")" || exit 1

notty=""
if [ "$1" = "--no-tty" ]; then
	notty=1
	shift
fi

if [ $# -gt 0 ]; then
	names="$*"
else
	names=$(for d in */; do echo "${d%/}"; done | grep -vx browser)
fi

out=$(mktemp)
trap 'rm -f "$out"' EXIT

failed=""
for name in $names; do
	[ -n "$notty" ] || clear
	echo "=== $name ==="
	if [ -n "$notty" ]; then
		go run "./$name" </dev/null >"$out" 2>&1
		status=$?
		cat "$out"
	else
		go run "./$name"
		status=$?
	fi
	# 130 = interrupted by ctrl+c (128+SIGINT); stop everything
	if [ "$status" -eq 130 ] || [ "$status" -eq 2 ]; then
		echo "interrupted in $name"
		exit 130
	fi
	[ "$status" -eq 0 ] || failed="$failed $name"
	[ -n "$notty" ] || sleep 2
done

if [ -n "$failed" ]; then
	echo "FAILED:$failed"
	exit 1
fi
