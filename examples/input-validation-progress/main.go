package main

import (
	"context"
	"errors"
	"time"

	"github.com/nfx/go-tui"
)

// main demonstrates validated prompts sharing terminal output with a progress bar.
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// a progress bar keeps advancing in the background while the user types
	// and while the validator below is busy.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range tui.NewSliceProgressBar(ctx, "indexing", make([]int, 1500)) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()

	// validators receive the prompt context, so a call to an external service
	// is cancelled together with the prompt.
	username, err := tui.Input(ctx, "Username:",
		tui.WithNonEmpty(),
		tui.WithRegex(`[a-z][a-z0-9]{2,15}`, "Use 3-16 lowercase letters or digits, starting with a letter"),
		tui.WithValidation(func(ctx context.Context, s string) error {
			// pretend to ask a remote service whether the name is taken
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				if s == "admin" || s == "root" {
					return errors.New("name is already taken")
				}
				return nil
			}
		}),
	)
	if err != nil {
		panic(err)
	}

	password, err := tui.Password(ctx, "Confirmation code:",
		tui.WithRegex(`[a-z0-9]{8,10}`, "Confirmation code between 8 and 10 alphanumeric characters"),
	)
	if err != nil {
		panic(err)
	}
	println(username, password)

	cancel()
	<-done
}
