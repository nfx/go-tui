package main

import (
	"context"
	"errors"
	"time"

	"github.com/nfx/go-tui"
)

func main() {
	ctx := context.Background()
	spinners, err := tui.NewSpinners(ctx)
	if err != nil {
		panic(err)
	}
	defer spinners.Close()

	// WithKeep leaves the last message on screen after the spinner is closed.
	build, err := spinners.Add(ctx, tui.WithPrefixf("%s", "build"), tui.WithKeep())
	if err != nil {
		panic(err)
	}
	deploy, err := spinners.Add(ctx, tui.WithPrefixf("deploy"))
	if err != nil {
		panic(err)
	}
	tests, err := spinners.Add(ctx, tui.WithPrefixf("tests"))
	if err != nil {
		panic(err)
	}

	// Add on a spinner nests a task under it. Closing a parent stops its
	// children, and WithNoElapsed hides the timer of a row.
	compile, err := build.Add(ctx, tui.WithPrefixf("compile"))
	if err != nil {
		panic(err)
	}
	link, err := build.Add(ctx, tui.WithPrefixf("link"), tui.WithNoElapsed())
	if err != nil {
		panic(err)
	}
	api, err := deploy.Add(ctx, tui.WithPrefixf("api"))
	if err != nil {
		panic(err)
	}
	layers := make([]*tui.Spinner, 2)
	for i := range layers {
		layers[i], err = api.Add(ctx, tui.WithPrefixf("layer %d", i+1))
		if err != nil {
			panic(err)
		}
	}
	web, err := deploy.Add(ctx, tui.WithPrefixf("web"))
	if err != nil {
		panic(err)
	}
	unit, err := tests.Add(ctx, tui.WithPrefixf("unit"))
	if err != nil {
		panic(err)
	}
	integration, err := tests.Add(ctx, tui.WithPrefixf("integration"))
	if err != nil {
		panic(err)
	}

	for i := 1; i <= 5; i++ {
		build.Updatef("building %d/5", i)
		compile.Updatef("packages %d/5", i)
		deploy.Updatef("updating services %d/5", i)
		api.Update("uploading image")
		for n, layer := range layers {
			layer.Updatef("pushing %d%%", min(i*25, (n+1)*50))
		}
		web.Update("restarting")
		tests.Updatef("running %d/5", i)
		unit.Updatef("%d suites", i)
		integration.Updatef("%d scenarios", i*2)
		// Column-aware truncation preserves clusters and closes styles.
		if i == 3 {
			web.Update("アップロード中 \U0001F468‍\U0001F469‍\U0001F467 \x1b[1;31mbold red and a very long tail that has to be truncated\x1b[0m 漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字")
		}
		time.Sleep(400 * time.Millisecond)
	}

	// A child ends alone: its parent and siblings keep running.
	compile.Close() //nolint:errcheck // example
	link.Update("linked")
	link.Close() //nolint:errcheck // example
	// The parent is kept, so the last message stays after Close.
	build.Update("build complete ✅")
	build.Close() //nolint:errcheck // example

	// Closing deploy stops api, its layers and web with it.
	deploy.Close() //nolint:errcheck // example

	// Fail marks a spinner as failed; its running children stop, and the
	// failure stays on screen.
	unit.Close() //nolint:errcheck // example
	integration.Fail(errors.New("2 scenarios failed"))
	tests.Fail(errors.New("tests failed"))
	time.Sleep(time.Second)
}
