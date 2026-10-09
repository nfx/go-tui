package main

import (
	"fmt"
	"os"
	"time"

	"github.com/nfx/go-tui"
)

type Service struct {
	Name     string
	Region   string
	Latency  int
	Uptime   float64
	Errors   float64
	Updated  time.Time
	Internal string
}

func main() {
	services := []Service{
		{"api", "eu-west", 120, 0.9991, 0.012, time.Now().Add(-2 * time.Hour), "x"},
		{"web", "us-east", 80, 0.9999, -0.003, time.Now().Add(-26 * time.Hour), "y"},
		{"jobs", "ap-south", 450, 0.9912, 0.045, time.Now().Add(-5 * time.Minute), "z"},
		{"auth", "eu-west", 35, 0.9998, 0.0, time.Now().Add(-72 * time.Hour), "w"},
	}

	// TableAuto derives columns from struct fields, and the options tweak them.
	err := tui.TableAuto(os.Stdout, services,
		tui.WithSkipColumns("Internal"),
		tui.WithFloat64AsPercent(),             // every float64 as a percentage
		tui.WithColumnGreenRedScale("Latency"), // low is green, high is red
		tui.WithColumnFormat("Latency", func(ms int) string {
			return fmt.Sprintf("%d ms", ms)
		}),
		tui.WithColumnTemplate("Updated", `{{ .Updated | ago }}`),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println()

	// Only selected columns, with +/- colored percentages.
	err = tui.TableAuto(os.Stdout, services,
		tui.WithIncludeColumns("Name", "Errors", "Uptime"),
		tui.WithColumnTypeFormat(func(t time.Time) string { return t.Format(time.Kitchen) }),
		tui.WithFloat64AsPercentPositiveColored(),
		tui.WithColumnRedGreenScale("Uptime"),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println()

	// Facts prints a single struct as a vertical list of field: value pairs.
	err = tui.Facts(os.Stdout, services[0], tui.WithSkipColumns("Internal"))
	if err != nil {
		panic(err)
	}
}
