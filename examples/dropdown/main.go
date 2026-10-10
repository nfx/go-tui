package main

import (
	"fmt"

	"github.com/nfx/go-tui"
)

func main() {
	// Items use terminal columns and remain whole clusters when clipped or filtered.
	items := []string{
		"1", "2", "3",
		"東京 (Tokyo)",
		"한국어 (Korean)",
		"cafe\u0301 (combining accent)",
		"\U0001F468\u200D\U0001F469\u200D\U0001F467 family (ZWJ sequence)",
		"\U0001F1FA\U0001F1E6 Ukraine (flag)",
		"\U0001F44D\U0001F3FD thumbs up (skin tone)",
		"1\uFE0F\u20E3 keycap",
		"red \x1b[31mANSI\x1b[0m colored item",
		" 漢字漢字漢字漢字漢字漢字 a very long item that does not fit into narrow terminals and has to be truncated",
		"invalid utf-8: \xff\xfe bytes",
	}
	for i := 21; i <= 30; i++ {
		items = append(items, fmt.Sprint(i))
	}
	_, err := tui.Dropdown("Select", items)
	if err != nil {
		panic(err)
	}
}
