// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/nfx/go-tui"
)

func ExampleDropdown_withStructs() {
	type Sample struct {
		Name string
		Age  int
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// presentation lives in the theme: a custom focus mark and active row
	theme := tui.DefaultTheme()
	theme.Symbols.Focus = "→"
	theme.Dropdown.Active = `{{ bold .Value }}{{ with .Details }} {{ dim . }}{{ end }}`
	if err := tui.SetDefaultTheme(theme); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	v, err := tui.Dropdown(ctx, "Samples", []Sample{
		{Name: "Alice", Age: 20},
		{Name: "Bob", Age: 30},
		{Name: "Василь", Age: 50}, // supports Unicode
		{Name: "Charlie", Age: 40},
	},
		// the projection picks the fields; details show on the active row only
		tui.WithTemplate(`.Name`, `"(age: " .Age ")"`),
		tui.WithHide(), // hides the dropdown after selection
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Hello, %s!\n", v.Name)
}

func ExampleDropdown_withLargeList() {
	raw, _ := os.ReadFile("/usr/share/dict/words") //nolint:errcheck // example
	words := strings.Split(string(raw), "\n")
	rand.Shuffle(len(words), func(i, j int) {
		words[i], words[j] = words[j], words[i]
	})
	// tens of thousands of words would nicely fit into a dropdown,
	// because of ".. more items" feature. It's also possible to
	// search for a word by typing its prefix.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, err := tui.Dropdown(ctx, "Select a word", words,
		tui.WithOneReturn())
	if err != nil {
		log.Fatal(err)
	}
	log.Println(v)
}

func ExampleNumber() {
	type Port uint16
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// the type argument chooses the result type and the accepted range
	workers, err := tui.Number[int](ctx, "Workers", tui.WithDefault("4"))
	if err != nil {
		log.Fatal(err)
	}
	size, err := tui.Number[uint64](ctx, "Bytes")
	if err != nil {
		log.Fatal(err)
	}
	port, err := tui.Number[Port](ctx, "Port", tui.WithDefault("8080"))
	if err != nil {
		log.Fatal(err)
	}
	// Up/Down change the decimal digit right of the caret: 1.|25 gives 1.35
	gain, err := tui.Number[float32](ctx, "Gain", tui.WithDefault("1.25"))
	if err != nil {
		log.Fatal(err)
	}
	log.Println(workers, size, port, gain)
}
