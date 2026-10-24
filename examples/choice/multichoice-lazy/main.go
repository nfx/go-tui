package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/nfx/go-tui"
)

// main demonstrates progressive choices with cancellation-aware production.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	seq := func(yield func(string, error) bool) {
		for _, name := range []string{
			"api", "worker", "scheduler", "gateway", "database", "cache",
			"queue", "broker", "auth", "billing", "search", "indexer",
			"notifier", "mailer", "storage", "proxy", "router", "monitor",
			"logger", "dashboard", "migrator",
		} {
			select {
			case <-ctx.Done():
				return
			case <-time.After(400 * time.Millisecond):
			}
			if !yield(name, nil) {
				return
			}
		}
	}
	selected, err := tui.MultichoiceLazy(ctx, "Components", seq, tui.WithDefault("api"))
	if err != nil {
		panic(err)
	}
	fmt.Println(selected)
}
