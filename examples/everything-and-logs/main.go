package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"time"

	"github.com/lmittmann/tint"
	"github.com/nfx/go-tui"
)

func main() {
	setupLogger()
	ctx, cancel := context.WithCancel(context.Background())
	logDone := startWordLogger(ctx)
	progressDone := startProgress(ctx)
	defer func() {
		cancel()
		<-progressDone
		<-logDone
	}()

	spinners, err := tui.NewSpinners(
		tui.WithContext(ctx), // TODO: require context by default
		tui.WithPrefixf("example"),
	)
	if errors.Is(err, io.EOF) {
		return
	}
	defer spinners.Close()

	runInteractiveExample(ctx, spinners)
}

func runInteractiveExample(ctx context.Context, spinners *tui.Spinners) {
	dropdownSpin := addBackgroundSpinner(ctx, spinners, "dropdown")
	lazyDropdownSpin := addBackgroundSpinner(ctx, spinners, "lazy dropdown")
	passwordSpin := addBackgroundSpinner(ctx, spinners, "password")
	inputTxtSpin := addBackgroundSpinner(ctx, spinners, "input")
	dropdownSpin.Update("inactive")
	lazyDropdownSpin.Update("inactive")
	passwordSpin.Update("inactive")
	inputTxtSpin.Update("inactive")

	flavor, ok := runStaticDropdown(ctx, dropdownSpin)
	if !ok {
		return
	}

	if !runLazyDropdown(ctx, lazyDropdownSpin) {
		return
	}

	if !runPassword(ctx, passwordSpin) {
		return
	}

	flavor, ok = runInput(ctx, inputTxtSpin, flavor)
	if !ok {
		return
	}
	_ = flavor
}

func addBackgroundSpinner(ctx context.Context, spinners *tui.Spinners, prefix string) *tui.Spinner {
	spinner, err := spinners.Add(ctx, tui.WithPrefixf("%s", prefix))
	if err != nil {
		panic(err)
	}
	return spinner
}

func runStaticDropdown(ctx context.Context, spinner *tui.Spinner) (string, bool) {
	spinner.Update("active")
	flavor, err := tui.Dropdown("Flavor:", []string{
		"apple", "banana", "cider", "pineapple", "pizza"},
		tui.WithContext(ctx))
	if errors.Is(err, io.EOF) {
		return "", false
	}
	closeSpinner(spinner)
	return flavor, true
}

func runLazyDropdown(ctx context.Context, spinner *tui.Spinner) bool {
	spinner.Update("active")
	_, err := tui.DropdownLazy("Lazy flavor:", randomLazyItems(ctx, 20))
	if errors.Is(err, io.EOF) {
		return false
	}
	closeSpinner(spinner)
	return true
}

func runPassword(ctx context.Context, spinner *tui.Spinner) bool {
	spinner.Update("active")
	_, err := tui.Password("Password:", tui.WithContext(ctx)) //nolint:contextcheck // example
	if errors.Is(err, io.EOF) {
		return false
	}
	closeSpinner(spinner)
	return true
}

func runInput(ctx context.Context, spinner *tui.Spinner, flavor string) (string, bool) {
	spinner.Update("active")
	flavor, err := tui.Input( //nolint:contextcheck // example
		"Confirm flavor:",
		tui.WithContext(ctx),
		tui.WithDefault(flavor))
	if errors.Is(err, io.EOF) {
		return "", false
	}
	closeSpinner(spinner)
	return flavor, true
}

func setupLogger() {
	slog.SetDefault(slog.New(
		tint.NewHandler(tui.Stderr(), &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
		}),
	))
}

func startWordLogger(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			slog.Info("background log",
				"a", rand.Intn(1000),
				"b", rand.Intn(1000),
				"c", rand.Intn(1000),
				"d", rand.Intn(1000),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(333 * time.Millisecond):
			}
		}
	}()
	return done
}

func startProgress(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range tui.NewSliceProgressBar("progress", make([]int, 10_000),
			tui.WithContext(ctx), // TODO: require context by default
		) {
			time.Sleep(1 * time.Millisecond)
		}
	}()
	return done
}

func randomLazyItems(ctx context.Context, n int) func(func(string, error) bool) {
	return func(yield func(string, error) bool) {
		for i := range n {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(50+rand.Intn(350)) * time.Millisecond):
			}
			if !yield(fmt.Sprintf("item %02d", i+1), nil) {
				return
			}
		}
	}
}

func closeSpinner(spinner *tui.Spinner) {
	if err := spinner.Close(); err != nil {
		slog.Error("close spinner", "err", err)
	}
}
