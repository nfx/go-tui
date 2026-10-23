// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package spinner

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/terst"
	"github.com/nfx/go-tui/theme"
)

// testClock is a clock of the group that moves only when the test says so.
type testClock struct{ elapsed atomic.Int64 }

func (c *testClock) now() time.Time { return testEpoch.Add(time.Duration(c.elapsed.Load())) }

func (c *testClock) advance(d time.Duration) { c.elapsed.Add(int64(d)) }

// settle returns once the group has handled everything sent before it, so that
// the clock moves only after the group has read it.
func settle(s *Spinners) { s.updateOffset(-1, "", nil) }

func (c *testClock) opt() option.Opt {
	return option.OpT(func(s *Spinners) error {
		s.now = c.now
		return nil
	})
}

func TestFormatElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{ //nolint:exhaustive // the stdlib units are not the cases
		-time.Second:                          "0s",
		0:                                     "0s",
		999 * time.Millisecond:                "0s",
		59*time.Second + 999*time.Millisecond: "59s",
		time.Minute:                           "1m00s",
		time.Minute + 5*time.Second:           "1m05s",
		59*time.Minute + 59*time.Second:       "59m59s",
		time.Hour:                             "1h00m00s",
		25*time.Hour + 2*time.Minute + time.Second: "25h02m01s",
	} {
		assert.Equal(t, want, formatElapsed(d))
	}
}

func treeSpinners(t *testing.T, o ...option.Opt) (*Spinners, *terst.Output, func(), *testClock) {
	t.Helper()
	clock := &testClock{}
	s, frames, tick, _ := themedSpinners(t, terst.ASCIITheme(), 40, append([]option.Opt{clock.opt()}, o...)...)
	return s, frames, tick, clock
}

func TestElapsedFreezesAtFirstTerminalTransition(t *testing.T) {
	s, frames, tick, clock := treeSpinners(t)
	build, err := s.Add(t.Context(), WithPrefixf("build"), WithKeep())
	assert.NoError(t, err)
	clock.advance(5 * time.Second)
	lint, err := s.Add(t.Context(), WithPrefixf("lint"))
	assert.NoError(t, err)
	build.Update("compiling")
	lint.Update("checking")
	clock.advance(7 * time.Second)
	tick()
	rows := frames.Next()
	assert.Contains(t, rows, "build: compiling (12s)")
	assert.Contains(t, rows, "lint: checking (7s)") // started later; updates do not reset it

	lint.Fail(errors.New("3 problems"))
	settle(s)
	clock.advance(time.Minute)
	assert.NoError(t, build.Close())
	settle(s)
	clock.advance(time.Minute)
	tick()
	rows = frames.Next()
	assert.Contains(t, rows, "- build: compiling (1m12s)")
	assert.Contains(t, rows, "! lint: 3 problems (7s)")

	// later calls change neither the status nor the time
	lint.Update("again")
	assert.NoError(t, lint.Close())
	tick()
	assert.Contains(t, frames.Next(), "! lint: 3 problems (7s)")
}

func TestElapsedOptions(t *testing.T) {
	s, frames, tick, clock := treeSpinners(t)
	off, err := s.Add(t.Context(), WithPrefixf("off"), WithNoElapsed())
	assert.NoError(t, err)
	on, err := s.Add(t.Context(), WithPrefixf("on"))
	assert.NoError(t, err)
	off.Update("quiet")
	on.Update("loud")
	clock.advance(3 * time.Second)
	tick()
	rows := frames.Next()
	assert.Contains(t, rows, "off: quiet\n")
	assert.Contains(t, rows, "on: loud (3s)\n")

	_, err = s.Add(t.Context(), option.OpT(func(*int) error { return nil }))
	assert.Error(t, err)
}

func TestElapsedInCustomTemplates(t *testing.T) {
	th := terst.ASCIITheme()
	th.Spinner.Running = `{{ .Message }}|{{ .Elapsed }}|{{ .Depth }}`
	clock := &testClock{}
	s, frames, tick, _ := themedSpinners(t, th, 40, clock.opt())
	parent, err := s.Add(t.Context())
	assert.NoError(t, err)
	child, err := parent.Add(t.Context())
	assert.NoError(t, err)
	parent.Update("p")
	child.Update("c")
	clock.advance(2 * time.Second)
	tick()
	// the group draws the branch, whatever the slot prints
	assert.Equal(t, clipped("\rp|2s|0\n\r\r` c|2s|1\n\r"), frames.Next())

	th.Spinner.Running = `{{ .Message }}`
	s2, frames2, tick2, _ := themedSpinners(t, th, 40, clock.opt())
	one, err := s2.Add(t.Context())
	assert.NoError(t, err)
	one.Update("plain")
	tick2()
	assert.Equal(t, clipped("\rplain\n\r"), frames2.Next()) // no timer unless the slot asks
}

func TestTreeRendersDepthFirst(t *testing.T) {
	th := terst.ASCIITheme()
	th.Symbols.Spinner = []string{"."}
	clock := &testClock{}
	s, frames, tick, _ := themedSpinners(t, th, 40, clock.opt())
	deploy, err := s.Add(t.Context(), WithPrefixf("deploy"))
	assert.NoError(t, err)
	check, err := s.Add(t.Context())
	assert.NoError(t, err)
	api, err := deploy.Add(t.Context(), WithPrefixf("api"))
	assert.NoError(t, err)
	web, err := deploy.Add(t.Context(), WithPrefixf("web"))
	assert.NoError(t, err)
	layers, err := api.Add(t.Context(), WithPrefixf("layers"))
	assert.NoError(t, err)
	deploy.Update("updating services")
	check.Update("checking configuration")
	api.Update("uploading image")
	web.Update("restarting")
	layers.Update("pushing layer 3")
	clock.advance(12 * time.Second)
	tick()
	assert.Equal(t, clipped("\r. deploy: updating services (12s)\n\r"+
		"\r| . api: uploading image (12s)\n\r"+
		"\r| ` . layers: pushing layer 3 (12s)\n\r"+
		"\r` . web: restarting (12s)\n\r"+
		"\r. checking configuration (12s)\n\r"), frames.Next())
}

func TestTreeIndentIsBounded(t *testing.T) {
	th := terst.ASCIITheme()
	th.Symbols.Spinner = []string{"."}
	s, frames, tick, _ := themedSpinners(t, th, 8, WithNoElapsed())
	sp, err := s.Add(t.Context())
	assert.NoError(t, err)
	for range 6 {
		sp, err = sp.Add(t.Context())
		assert.NoError(t, err)
	}
	sp.Update("deep")
	tick()
	rows := strings.Split(strings.TrimSuffix(frames.Next(), "\n\r\x1b[?7h"), "\n\r")
	assert.Equal(t, 7, len(rows))
	assert.Equal(t, "\r      …", rows[6]) // clipped to one physical row, nothing else fits
}

func TestParentCloseStopsChildren(t *testing.T) {
	s, frames, tick, _ := treeSpinners(t, WithNoElapsed())
	parent, err := s.Add(t.Context(), WithPrefixf("parent"))
	assert.NoError(t, err)
	child, err := parent.Add(t.Context(), WithPrefixf("child"))
	assert.NoError(t, err)
	sibling, err := s.Add(t.Context(), WithPrefixf("sibling"))
	assert.NoError(t, err)
	tick()
	assert.Contains(t, frames.Next(), "child")

	assert.NoError(t, parent.Close())
	tick()
	out := frames.Next()
	assert.True(t, !strings.Contains(out, "parent") && !strings.Contains(out, "child"))
	assert.Contains(t, out, "sibling")

	// the stopped branch is gone, and takes no children
	_, err = child.Add(t.Context())
	assert.ErrorIs(t, err, ErrSpinnerStopped)
	_, err = parent.Add(t.Context())
	assert.ErrorIs(t, err, ErrSpinnerStopped)
	assert.NoError(t, child.Close()) // safe on a removed handle
	assert.NoError(t, parent.Close())
	sibling.Update("still running")
	tick()
	assert.Contains(t, frames.Next(), "sibling: still running")
}

func TestChildLifecycleIsIsolated(t *testing.T) {
	s, frames, tick, _ := treeSpinners(t, WithNoElapsed())
	parent, err := s.Add(t.Context(), WithPrefixf("parent"))
	assert.NoError(t, err)
	a, err := parent.Add(t.Context(), WithPrefixf("alpha"))
	assert.NoError(t, err)
	b, err := parent.Add(t.Context(), WithPrefixf("beta"))
	assert.NoError(t, err)
	a.Fail(errors.New("boom"))
	assert.NoError(t, b.Close())
	tick()
	out := frames.Next()
	assert.Contains(t, out, "parent")
	assert.Contains(t, out, "! alpha: boom")
	assert.True(t, !strings.Contains(out, "beta"))

	// a failed spinner takes no more children
	_, err = a.Add(t.Context())
	assert.ErrorIs(t, err, ErrSpinnerStopped)
}

func TestParentFailureStopsChildrenNeutrally(t *testing.T) {
	s, frames, tick, clock := treeSpinners(t)
	parent, err := s.Add(t.Context(), WithPrefixf("parent"))
	assert.NoError(t, err)
	live, err := parent.Add(t.Context(), WithPrefixf("live"))
	assert.NoError(t, err)
	kept, err := parent.Add(t.Context(), WithPrefixf("kept"), WithKeep())
	assert.NoError(t, err)
	bad, err := parent.Add(t.Context(), WithPrefixf("bad"))
	assert.NoError(t, err)
	clock.advance(2 * time.Second)
	bad.Fail(errors.New("first"))
	settle(s)
	clock.advance(3 * time.Second)
	parent.Fail(errors.New("second"))
	settle(s)
	clock.advance(time.Minute)
	tick()
	out := frames.Next()
	assert.Contains(t, out, "! parent: second (5s)")
	assert.Contains(t, out, "| - kept (5s)")
	assert.Contains(t, out, "` ! bad: first (2s)") // keeps its own status and time
	assert.True(t, !strings.Contains(out, "live"))
	assert.True(t, strings.Index(out, "kept") < strings.Index(out, "bad"))

	_, err = parent.Add(t.Context())
	assert.ErrorIs(t, err, ErrSpinnerStopped)
	assert.NoError(t, live.Close())
	assert.NoError(t, kept.Close())
}

func TestStoppedAncestorAnchorsRetainedChild(t *testing.T) {
	th := terst.ASCIITheme()
	th.Symbols.Spinner = []string{"."}
	clock := &testClock{}
	s, frames, tick, _ := themedSpinners(t, th, 40, clock.opt(), WithNoElapsed())
	root, err := s.Add(t.Context())
	assert.NoError(t, err)
	mid, err := root.Add(t.Context())
	assert.NoError(t, err)
	leaf, err := mid.Add(t.Context(), WithPrefixf("leaf"), WithKeep())
	assert.NoError(t, err)
	_, err = mid.Add(t.Context(), WithPrefixf("gone"))
	assert.NoError(t, err)
	leaf.Update("done")
	assert.NoError(t, root.Close())
	tick()
	// nameless anchors show only their neutral frame
	assert.Equal(t, clipped("\r-\n\r\r` -\n\r\r  ` - leaf: done\n\r"), frames.Next())
}

func TestChildAddRacesWithGroupClose(t *testing.T) {
	s, _, _, _ := treeSpinners(t) //nolint:dogsled // only the group matters
	parent, err := s.Add(t.Context())
	assert.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := parent.Add(t.Context()); err != nil {
				return
			}
		}
	}()
	assert.NoError(t, parent.Close())
	terst.Recv(t, done)
	s.Close()
	s.wg.Wait() // every accepted spinner ended exactly once
}

func TestNestedLogsAndEvents(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	events := make(chan spinnerEvent, 32)
	out := terst.NewOutput(t)
	terst.UseIO(t, nil, out)
	s, err := NewSpinners(t.Context(), option.OpT(func(s *Spinners) error {
		s.eventSink = func(ev spinnerEvent) { events <- ev }
		return nil
	}))
	assert.NoError(t, err)

	parent, err := s.Add(t.Context(), WithPrefixf("deploy"))
	assert.NoError(t, err)
	child, err := parent.Add(t.Context(), WithPrefixf("api"))
	assert.NoError(t, err)
	bad, err := parent.Add(t.Context(), WithPrefixf("web"))
	assert.NoError(t, err)
	bad.Fail(errors.New("boom"))
	child.Update("pushing")
	assert.NoError(t, parent.Close())
	s.Close()

	type record struct {
		Message string `json:"msg"`
		Spinner int    `json:"spinner"`
		Prefix  string `json:"prefix"`
		Status  string `json:"status"`
		Parent  *int   `json:"parent"`
		Depth   *int   `json:"depth"`
		Elapsed any    `json:"elapsed"`
	}
	var got []record
	decoder := json.NewDecoder(&logs)
	for decoder.More() {
		var entry record
		assert.NoError(t, decoder.Decode(&entry))
		got = append(got, entry)
	}
	zero, one := 0, 1
	assert.Equal(t, []record{
		{"spinner started", 0, "deploy", "started", nil, nil, nil},
		{"spinner started", 1, "api", "started", &zero, &one, nil},
		{"spinner started", 2, "web", "started", &zero, &one, nil},
		{"boom", 2, "web", "failed", &zero, &one, nil},
		{"pushing", 1, "api", "running", &zero, &one, nil},
		{"spinner stopped", 0, "deploy", "stopped", nil, nil, nil}, // parent first, no record for web
		{"spinner stopped", 1, "api", "stopped", &zero, &one, nil},
	}, got)

	var added []spinnerAdded
	for len(events) > 0 {
		if ev, ok := (<-events).(spinnerAdded); ok {
			added = append(added, ev)
		}
	}
	assert.Equal(t, 3, len(added))
	assert.Equal(t, -1, added[0].Parent)
	assert.Equal(t, 0, added[1].Parent)
}

func TestNestedTemplateDataCompiles(t *testing.T) {
	// the built-in rows must not need caller theme data to hide the timer
	th := terst.ASCIITheme()
	th.Spinner = theme.Spinner{Interval: time.Hour}
	s, frames, tick, _ := themedSpinners(t, th, 40, WithNoElapsed())
	sp, err := s.Add(t.Context())
	assert.NoError(t, err)
	sp.Update("x")
	tick()
	assert.Contains(t, frames.Next(), "x\n")
}

func TestTreeLargerThanTerminalShowsOverflow(t *testing.T) {
	th := terst.ASCIITheme()
	th.Symbols.Spinner = []string{"."}
	clock := &testClock{}
	s, frames, tick, _ := themedSpinners(t, th, 20, clock.opt(), WithNoElapsed())
	root, err := s.Add(t.Context(), WithPrefixf("root"))
	assert.NoError(t, err)
	for i := range 12 {
		_, err = root.Add(t.Context(), WithPrefixf("n%d", i))
		assert.NoError(t, err)
	}
	tick()
	// the terminal has 10 rows: 8 spinners, the overflow and the cursor row
	assert.Equal(t, clipped("\r. root\n\r"+
		"\r| . n0\n\r\r| . n1\n\r\r| . n2\n\r\r| . n3\n\r\r| . n4\n\r\r| . n5\n\r\r| . n6\n\r"+
		"\r. 5 more\n\r"), frames.Next())
}
