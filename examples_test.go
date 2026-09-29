// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui_test

import (
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
	v, err := tui.Dropdown("Samples", []Sample{
		{Name: "Alice", Age: 20},
		{Name: "Bob", Age: 30},
		{Name: "Василь", Age: 50}, // supports Unicode
		{Name: "Charlie", Age: 40},
	}, tui.WithTimeout(60*time.Second),
		// template overrides allow rendering different struct fields and methods
		tui.WithActiveItemTemplate(`→ {{.Name}} {{ dim "(age: " .Age ")" }}`),
		tui.WithInactiveItemTemplate(`~ {{.Name}}`),
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
	v, err := tui.Dropdown("Select a word", words,
		tui.WithTimeout(30*time.Second),
		tui.WithOneReturn())
	if err != nil {
		log.Fatal(err)
	}
	log.Println(v)
}
