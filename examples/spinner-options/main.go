package main

import (
	"context"
	"errors"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	spinners, err := tui.NewSpinners()
	if err != nil {
		panic(err)
	}
	defer spinners.Close()

	// WithKeep leaves the last message on screen after the spinner is closed.
	kept, err := spinners.Add(context.Background(),
		tui.WithPrefixf("%s", "build"),
		tui.WithKeep())
	if err != nil {
		panic(err)
	}
	// WithFrames replaces the animation, see also tui.SpinnerStyleDocs.
	custom, err := spinners.Add(context.Background(),
		tui.WithPrefixf("deploy"),
		tui.WithFrames(tui.SpinnerStyleDocs))
	if err != nil {
		panic(err)
	}
	failing, err := spinners.Add(context.Background(), tui.WithPrefixf("tests"))
	if err != nil {
		panic(err)
	}

	for i := 1; i <= 5; i++ {
		kept.Updatef("compiling %d/5", i)
		custom.Updatef("uploading %d/5", i)
		failing.Updatef("running %d/5", i)
		// Column-aware truncation preserves clusters and closes styles.
		if i == 3 {
			custom.Update("アップロード中 \U0001F468\u200D\U0001F469\u200D\U0001F467 \x1b[1;31mbold red and a very long tail that has to be truncated\x1b[0m 漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字")
		}
		time.Sleep(400 * time.Millisecond)
	}
	kept.Update("build complete \u2705")
	kept.Close()   //nolint:errcheck // example
	custom.Close() //nolint:errcheck // example
	// Fail marks the spinner as failed with the error.
	failing.Fail(errors.New("3 tests failed"))
	time.Sleep(time.Second)
}
