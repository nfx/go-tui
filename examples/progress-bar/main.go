package main

import (
	"fmt"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	// NewSliceProgressBar advances as the iterator is consumed.
	// Column-aware labels keep the bar aligned with wide characters and emoji.
	files := make([]string, 300)
	for range tui.NewSliceProgressBar("ファイル走査 \U0001F50D", files) {
		time.Sleep(5 * time.Millisecond)
	}

	// NewMaxProgressBar is advanced manually, for work measured in arbitrary units.
	// WithFormatRate customizes how the throughput is displayed.
	bar, err := tui.NewMaxProgressBar("ダウンロード cafe\u0301", 1000,
		tui.WithFormatRate(func(perSecond float64) string {
			return fmt.Sprintf("%.0f KB/s", perSecond)
		}))
	if err != nil {
		panic(err)
	}
	for range 100 {
		bar.Add(10)
		time.Sleep(20 * time.Millisecond)
	}
	if err := bar.Close(); err != nil {
		panic(err)
	}
}
