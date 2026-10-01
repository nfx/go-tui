package main

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lmittmann/tint"
	"github.com/nfx/go-tui"
)

//nolint:funlen,gocognit,cyclop // example
func main() {
	ctx := context.Background()
	w, err := tui.NewIO(ctx) //nolint:all // ...
	if err != nil {
		panic(err)
	}

	s, err := tui.NewSpinners(tui.WithOutput(w), tui.WithContext(ctx))
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			local := s.MustAddBackground(tui.WithPrefixf("spinner %d", j))
			defer local.Close() //nolint:errcheck // example
			for k := range 10 {
				local.Updatef("task %d", k)
				time.Sleep(time.Duration(rand.Intn(500)) * time.Millisecond)
			}
			local.Update("Done")
		}(i)
	}
	wg.Wait()

	first := s.MustAddBackground()
	first.Update("Loading...")

	second := s.MustAddBackground()
	second.Update("Also loading...")

	// set global logger with custom options
	slog.SetDefault(slog.New(
		tint.NewHandler(w, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
		}),
	))

	done := make(chan struct{})

	go func() {
		var counter int
		var third *tui.Spinner
		raw, _ := os.ReadFile("/usr/share/dict/words") //nolint:errcheck // example
		words := strings.Split(string(raw), "\n")
		ticks := time.NewTicker(333 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks.C:
				counter++
				word := words[rand.Intn(len(words))]
				first.Update("word of the first: " + word)
				if counter == 10 {
					first.Close() //nolint:errcheck // example
				}
				if counter > 11 {
					if third == nil {
						third = s.MustAddBackground() //nolint:contextcheck // example
					}
					third.Updatef("New spinner for counter: %d", counter)
				}
				if counter == 30 {
					s.Close()
				}
				if counter == 50 {
					done <- struct{}{}
				}
				slog.Info("word of the second", "word", word)
			}
		}
	}()

	// tui.Confirm("Do you want to continue?", tui.WithOutput(w), tui.WithContext(ctx))

	<-done
}
