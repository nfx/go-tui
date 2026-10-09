package main

import (
	"io"
	"os"

	"github.com/nfx/go-tui"
)

// Usage: go run ./progress-reader <file>
func main() {
	if len(os.Args) < 2 {
		println("usage: progress-reader <file>")
		return
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()

	// the reader reports its size via Stat(), and shows rate and estimate
	r, err := tui.NewFileProgressReader(f, "Reading")
	if err != nil {
		panic(err)
	}
	defer r.Close()

	n, err := io.Copy(io.Discard, r)
	if err != nil {
		panic(err)
	}
	println("read bytes:", n)
}
