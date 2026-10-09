package main

import (
	"context"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	// shows a spinner with the message for as long as the callback runs.
	err := tui.LongRunning(context.Background(), "Warming up the cache…", func(ctx context.Context) error {
		select {
		case <-time.After(3 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		panic(err)
	}
}
