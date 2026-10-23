package main

import (
	"context"
	"sync"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	// every bar is advanced at its own pace, so the rate and the estimate
	// shown for each one differ while they all update at the same time.
	// The sizes differ but the work takes as long for all of them, so no bar
	// leaves the screen before the others are done.
	const duration = 8 * time.Second
	stages := []struct {
		label string
		size  int64
	}{
		{"Downloading", 500},
		{"Extracting", 200},
		{"Indexing", 100},
	}
	var wg sync.WaitGroup
	var bars []*tui.Progressbar
	for _, s := range stages {
		bar, err := tui.NewMaxProgressBar(ctx, s.label, s.size)
		if err != nil {
			panic(err)
		}
		bars = append(bars, bar)
		wg.Go(func() {
			delay := duration / time.Duration(s.size)
			for range s.size {
				bar.Add(1)
				time.Sleep(delay)
			}
		})
	}
	wg.Wait()
	// closing removes a bar, so close them together once all are complete
	for _, bar := range bars {
		if err := bar.Close(); err != nil {
			panic(err)
		}
	}
}
