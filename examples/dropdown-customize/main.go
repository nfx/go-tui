package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/nfx/go-tui"
	"golang.org/x/term"
)

func main() {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		panic(err)
	}
	slog.Info("got terminal", "w", width, "h", height)

	v, err := tui.Dropdown("Samples", []Sample{
		{Name: "Alice", Age: 20},
		{Name: "Bob", Age: 30},
		{Name: "Василь", Age: 50},
		{Name: "Charlie", Age: 40},
	}, tui.WithTimeout(60*time.Second),
		tui.WithActiveItemTemplate(`→ {{.Name}} {{ dim "(age: " .Age ")" }}`),
		tui.WithInactiveItemTemplate(`~ {{.Name}}`),
		tui.WithHide(),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Hello, %s!\n", v.Name)
}

type Sample struct {
	Name string
	Age  int
}
