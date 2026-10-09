package main

import (
	"fmt"
	"time"

	"github.com/nfx/go-tui"
)

type Region struct {
	Zone string
	Code int
}

func main() {
	// keys are shown in the dropdown (sorted), values are returned alongside.
	k, v, err := tui.DropdownKV("Environment", map[string]string{
		"development": "dev.example.com",
		"staging":     "stage.example.com",
		"production":  "example.com",
	}, tui.WithTimeout(60*time.Second))
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s -> %s\n", k, v)

	// WithTemplate picks the main field shown for all item states,
	// and the extra fields shown in parentheses for the active item.
	region, err := tui.Dropdown("Region", []Region{
		{"eu-west", 1},
		{"us-east", 2},
		{"ap-south", 3},
	}, tui.WithTemplate(".Zone", ".Code"))
	if err != nil {
		panic(err)
	}
	fmt.Println("selected", region.Zone)
}
