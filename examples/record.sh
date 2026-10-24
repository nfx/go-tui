#!/bin/sh
# Records the example in the current directory as demo.gif.
# Usage: cd examples/<name> && ../record.sh
set -e

WORKSPACE=$(cd "$(dirname "$0")/.." && pwd)
make -s -C "$WORKSPACE" "$WORKSPACE/.bin/asciinema" "$WORKSPACE/.bin/agg"

rm -f "$WORKSPACE/.bin/demo.cast"
"$WORKSPACE/.bin/asciinema" rec \
    --command "go run main.go" \
    --capture-input \
    --window-size 80x24 \
    "$WORKSPACE/.bin/demo.cast"
"$WORKSPACE/.bin/agg" "$WORKSPACE/.bin/demo.cast" "$PWD/demo.gif"
rm -f "$WORKSPACE/.bin/demo.cast"

if [ ! -f README.md ]; then
    printf '# %s\n\n![demo](demo.gif)\n' "$(basename "$PWD")" > README.md
fi
