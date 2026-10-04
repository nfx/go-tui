package main

import (
	"os"

	"github.com/nfx/go-tui"
)

func main() {
	// // Different shades of blue using 256-color ANSI codes
	// shadesOfBlue := []int{21, 27, 33, 39, 45, 51, 87, 123, 159, 195}
	// for _, shade := range shadesOfBlue {
	// 	prefix := fmt.Sprintf("\x1b[38;5;%dm", shade)
	// 	fmt.Printf(prefix+"%#v,\033[0m\n", prefix)
	// }
	// // Different shades of green using 256-color ANSI codes
	// shadesOfGreen := []int{22, 28, 34, 40, 46, 82, 118, 154, 190, 226}
	// for _, shade := range shadesOfGreen {
	// 	prefix := fmt.Sprintf("\x1b[38;5;%dm", shade)
	// 	fmt.Printf(prefix+"%#v,\033[0m\n", prefix)
	// }
	data := map[string]interface{}{
		"name":    "John Doe",
		"age":     30,
		"address": map[string]string{"city": `New "York"`, "zip": "10001"},
		"hobbies": []string{"reading", "traveling", "coding"},
		"stuff": []interface{}{
			map[string]interface{}{
				"name":    "John Doe",
				"age":     30,
				"address": map[string]string{"city": `New "York"`, "zip": "10001"},
				"hobbies": []string{"reading", "traveling", "coding"},
				"stuff": []interface{}{1, "two", 3.14, true, map[string]interface{}{
					"name":    "John Doe",
					"age":     30,
					"address": map[string]string{"city": `New "York"`, "zip": "10001"},
					"hobbies": []string{"reading", "traveling", "coding"},
					"stuff":   []interface{}{1, "two", 3.14, true, nil},
				}},
			}, "two", 3.14, true, nil},
	}
	tui.PrettyJSON(os.Stdout, data) //nolint:errcheck // example
}
