package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nfx/go-tui"
)

// main demonstrates ordered validation rules that keep rejected text editable.
func main() {
	ctx := context.Background()
	// a rejected answer stays in the prompt: the message appears under the
	// input and the typed text is kept. Rules run in order, first failure wins.
	name, err := tui.Input(ctx, "Username (letters and digits):",
		tui.WithNonEmpty(),
		tui.WithRegex(`[a-z][a-z0-9]{2,15}`, "Use 3-16 lowercase letters or digits, starting with a letter"),
		tui.WithValidation(func(_ context.Context, s string) error {
			if strings.HasPrefix(s, "admin") {
				return errors.New("This name is reserved")
			}
			return nil
		}),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("Welcome,", name)
}
