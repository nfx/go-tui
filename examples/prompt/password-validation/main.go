package main

import (
	"context"
	"errors"
	"unicode"

	"github.com/nfx/go-tui"
)

// main demonstrates password validation with feedback that does not expose the secret.
func main() {
	ctx := context.Background()
	result, err := tui.Password(ctx, "New password:",
		tui.WithValidation(func(_ context.Context, s string) error {
			if len([]rune(s)) < 8 {
				return errors.New("Use at least 8 characters")
			}
			for _, r := range s {
				if unicode.IsDigit(r) {
					return nil
				}
			}
			// messages must never include the password itself
			return errors.New("Add at least one digit")
		}),
	)
	if err != nil {
		panic(err)
	}
	println("Accepted a password of", len([]rune(result)), "runes")
}
