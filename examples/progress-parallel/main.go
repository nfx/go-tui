package main

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	urls := make([]string, 60)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.com/%d", i)
	}
	// callback runs on multiple goroutines; the first returned error stops the rest.
	err := tui.NewParallelProgressBar("Fetching", urls, func(url string) error {
		time.Sleep(time.Duration(50+rand.IntN(200)) * time.Millisecond)
		return nil
	}, tui.WithWorkers(8))
	if err != nil {
		panic(err)
	}
}
