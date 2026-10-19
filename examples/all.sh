#!/bin/sh
# Runs every example in sequence (except browser). Usage: ./all.sh [name ...]
cd "$(dirname "$0")" || exit 1

if [ $# -gt 0 ]; then
	names="$*"
else
	names=$(for d in */; do echo "${d%/}"; done | grep -vx browser)
fi

failed=""
for name in $names; do
	clear
	echo "=== $name ==="
	go run "./$name"
	status=$?
	# 130 = interrupted by ctrl+c (128+SIGINT); stop everything
	if [ "$status" -eq 130 ] || [ "$status" -eq 2 ]; then
		echo "interrupted in $name"
		exit 130
	fi
	[ "$status" -eq 0 ] || failed="$failed $name"
    sleep 2
done

if [ -n "$failed" ]; then
	echo "FAILED:$failed"
	exit 1
fi
