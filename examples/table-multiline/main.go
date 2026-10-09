package main

import (
	"fmt"
	"os"

	"github.com/nfx/go-tui"
)

type Task struct {
	Name     string
	Steps    string
	Attempts int
}

func main() {
	tasks := []Task{
		{"build", "fetch deps\ncompile\nlink", 1},
		{"test", "unit\nintegration", 3},
		{"deploy", "upload", 2},
	}

	// By default, line breaks inside cells are rendered as spaces,
	// so every record stays on a single line and gets truncated.
	err := tui.TableAuto(os.Stdout, tasks,
		tui.WithColumnGreenRedScale("Attempts"))
	if err != nil {
		panic(err)
	}
	fmt.Println()

	// WithMultilineCells renders each line of a cell on its own physical row,
	// aligned with the column, while colors stay with their cells.
	err = tui.TableAuto(os.Stdout, tasks,
		tui.WithColumnGreenRedScale("Attempts"),
		tui.WithMultilineCells())
	if err != nil {
		panic(err)
	}
}
