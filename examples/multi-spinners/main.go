package main

import (
	"context"
	"log"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	sm, err := tui.NewSpinners(ctx)
	if err != nil {
		log.Fatal(err)
	}

	first, err := sm.Add(ctx)
	if err != nil {
		log.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(ctx)
	second, err := sm.Add(ctx2)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		first.Update("Processing task 1... A")
		time.Sleep(2 * time.Second)
		third, err := sm.Add(ctx)
		if err != nil {
			log.Fatal(err)
		}
		third.Update("Processing task 3... A")
		second.Update("Processing task 2... A")
		time.Sleep(2 * time.Second)
		first.Close() //nolint:errcheck // removes first spinner
		third.Update("Processing task 3... B")
	}()

	go func() {
		second.Update("Processing task 2... B")
		time.Sleep(3 * time.Second)
		second.Update("Processing task 2... C")
		time.Sleep(2 * time.Second)
		cancel2() // removes second spinner
	}()

	// Wait for all spinners to complete
	time.Sleep(10 * time.Second)
	sm.Close()
}
