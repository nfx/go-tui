#!/bin/sh
# Records the example in the current directory as site/static/demos/<path>.gif.
# Usage: cd examples/<group>/<name> && ../../record.sh
set -e

WORKSPACE=$(cd "$(dirname "$0")/.." && pwd)
REL=${PWD#"$WORKSPACE/examples/"}
GIF="$WORKSPACE/site/static/demos/$REL.gif"
mkdir -p "$(dirname "$GIF")"
make -s -C "$WORKSPACE" "$WORKSPACE/.bin/asciinema" "$WORKSPACE/.bin/agg"

rm -f "$WORKSPACE/.bin/demo.cast"
"$WORKSPACE/.bin/asciinema" rec \
    --command "go run main.go" \
    --capture-input \
    --window-size 80x24 \
    "$WORKSPACE/.bin/demo.cast"
"$WORKSPACE/.bin/agg" "$WORKSPACE/.bin/demo.cast" "$GIF"
rm -f "$WORKSPACE/.bin/demo.cast"

if [ ! -f README.md ]; then
    UP=..
    for _ in $(echo "$REL" | tr '/' ' '); do UP=$UP/..; done
    printf '# %s\n\n![demo](%s/site/static/demos/%s.gif)\n' "$(basename "$PWD")" "$UP" "$REL" > README.md
fi
