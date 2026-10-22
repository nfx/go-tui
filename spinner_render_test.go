// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"github.com/nfx/go-tui/internal/option"
	"github.com/nfx/go-tui/internal/term"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/terst"
)

func TestThemedSpinnerStates(t *testing.T) {
	s, frames, tick, restored := themedSpinners(t, asciiTheme(false), 40)
	build, err := s.Add(t.Context(), WithPrefixf("build"), WithKeep())
	assert.NoError(t, err)
	lint, err := s.Add(t.Context(), WithPrefixf("lint"))
	assert.NoError(t, err)
	docs, err := s.Add(t.Context())
	assert.NoError(t, err)
	build.Update("compiling")
	lint.Update("checking")
	docs.Update("writing")
	tick()
	assert.Equal(t, "\r- build: compiling\n\r\r\\ lint: checking\n\r\r| writing\n\r", <-frames)

	// kept after Close is stopped, not a success; failed stays; the rest goes
	assert.NoError(t, build.Close())
	lint.Fail(errors.New("3 problems"))
	assert.NoError(t, docs.Close())
	tick()
	rows := "\r- build: compiling\n\r\r! lint: 3 problems\n\r"
	assert.Equal(t, "\x1b[3A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1B\r\x1b[K\x1b[2A\r"+rows, <-frames)
	// ended spinners no longer animate
	tick()
	assert.Equal(t, "\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r"+rows, <-frames)

	go s.Close()
	assert.Equal(t, "\x1b[2A\r\x1b[K\x1b[1B\r\x1b[K\x1b[1A\r", untilStopped(t, s, frames))
	assert.True(t, restored.Load())
}

func TestThemedSpinnerFramesAndGroupTheme(t *testing.T) {
	th := asciiTheme(false)
	th.Symbols.Spinner = []string{"a", "b"}
	s, frames, tick, _ := themedSpinners(t, th, 40)
	first, err := s.Add(t.Context())
	assert.NoError(t, err)
	first.Update("theme")

	// a theme installed later reaches neither the group nor its later spinners
	later := asciiTheme(false)
	later.Symbols.Spinner = []string{"x"}
	later.Templates.Spinner.Running = `changed`
	assert.NoError(t, term.Default.InstallTheme(later))
	added, err := s.Add(t.Context())
	assert.NoError(t, err)
	added.Update("later")
	tick()
	assert.Equal(t, "\ra theme\n\r\rb later\n\r", <-frames)
}

func TestThemedSpinnerSlotsAndView(t *testing.T) {
	th := asciiTheme(false)
	th.Templates.Spinner.Running = `{{ .State }} {{ .Frame }} {{ .Prefix }}={{ .Message }}`
	th.Templates.Spinner.Failed = `{{ .State }} {{ .Frame }} {{ .Message }}`
	th.Symbols.Spinner = []string{"."}
	s, frames, tick, _ := themedSpinners(t, th, 40)
	sp, err := s.Add(t.Context(), WithPrefixf("p"))
	assert.NoError(t, err)
	sp.Update("line\none\ttab\x1b[2K")
	tick()
	assert.Equal(t, "\rrunning . p=line one tab\n\r", <-frames)
	sp.Fail(errors.New("bad"))
	tick()
	// the failed row shows the frame it stopped at
	assert.Equal(t, "\x1b[1A\r\x1b[K\rfailed . bad\n\r", <-frames)
}

func TestThemedSpinnerClipsBeforeLastColumn(t *testing.T) {
	s, frames, tick, _ := themedSpinners(t, asciiTheme(false), 10)
	sp, err := s.Add(t.Context())
	assert.NoError(t, err)
	sp.Update("a long message")
	tick()
	assert.Equal(t, "\r- a long…\n\r", <-frames)
}

func TestThemedSpinnerRoles(t *testing.T) {
	terst.WithEnv(t, "FORCE_COLOR=3")
	th := asciiTheme(false)
	th.Light = true
	th.Symbols.Spinner = []string{"*"}
	s, frames, tick, _ := themedSpinners(t, th, 40)
	run, err := s.Add(t.Context())
	assert.NoError(t, err)
	run.Update("run")
	kept, err := s.Add(t.Context(), WithKeep())
	assert.NoError(t, err)
	assert.NoError(t, kept.Close())
	failed, err := s.Add(t.Context())
	assert.NoError(t, err)
	failed.Fail(errors.New("no"))
	tick()
	out := <-frames
	roles := term.Default.BindTheme(t.Context(), term.Default.CurrentTheme(), s.Out, nil).Roles
	assert.True(t, len(roles.Accent) > 0 && len(roles.Muted) > 0 && len(roles.Failure) > 0)
	assert.Contains(t, out, string(roles.Accent)+"*")
	assert.Contains(t, out, string(roles.Muted)+"-")
	assert.Contains(t, out, string(roles.Failure)+"!")
	// Close is not success
	assert.True(t, !strings.Contains(out, string(roles.Success)))
}

func TestThemedSpinnerRenderErrorEndsGroup(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Spinner.Running = `{{ if eq .Message "bad" }}{{ boom }}{{ end }}{{ .Message }}`
	s, frames, tick, restored := themedSpinners(t, th, 40)
	sp, err := s.Add(t.Context())
	assert.NoError(t, err)
	sp.Update("ok")
	tick()
	assert.Equal(t, "\rok\n\r", <-frames)

	sp.Update("bad")
	tick()
	// no partial frame: only the drawn row is cleared
	assert.Equal(t, "\x1b[1A\r\x1b[K", untilStopped(t, s, frames))
	assert.True(t, restored.Load())
	_, err = s.Add(t.Context())
	assert.Contains(t, err.Error(), "theme.Templates.Spinner.Running")
	assert.Contains(t, err.Error(), "boom")
	err = sp.Close()
	assert.Contains(t, err.Error(), "boom")
	sp.Update("ignored") // does not block
}

func TestThemedSpinnerWriteErrorEndsGroup(t *testing.T) {
	gone := errors.New("terminal gone")
	var released atomic.Bool
	s, frames, tick, _ := themedSpinners(t, asciiTheme(false), 40, option.OpT(func(s *Spinners) error {
		s.makeTermIO = func(in io.Reader, _ io.Writer) (*term.IO, error) {
			return &term.IO{
				In:     in,
				Out:    terst.FailingWriter{Err: gone},
				Width:  40,
				Height: 10,
				Restore: func() error {
					released.Store(true)
					return nil
				},
			}, nil
		}
		return nil
	}))
	_, err := s.Add(t.Context())
	assert.NoError(t, err)
	tick()
	assert.Equal(t, "", untilStopped(t, s, frames))
	assert.True(t, released.Load())
	_, err = s.Add(t.Context())
	assert.ErrorIs(t, err, gone)
}

func TestThemedSpinnerInterval(t *testing.T) {
	realTicks := option.OpT(func(s *Spinners) error {
		s.ticks = s.ticker.C
		return nil
	})
	th := asciiTheme(false)
	th.Spinner.Interval = time.Millisecond
	s, frames, _, _ := themedSpinners(t, th, 40, realTicks)
	_, err := s.Add(t.Context())
	assert.NoError(t, err)
	select {
	case <-frames:
	case <-time.After(time.Second):
		t.Fatal("no frame at the theme interval")
	}
	go s.Close()
	untilStopped(t, s, frames)

	th.Spinner.Interval = time.Hour
	s, frames, _, _ = themedSpinners(t, th, 40, realTicks)
	_, err = s.Add(t.Context())
	assert.NoError(t, err)
	select {
	case out := <-frames:
		t.Fatalf("frame before the theme interval: %q", out)
	case <-time.After(300 * time.Millisecond): // three legacy intervals
	}
}

func TestThemedSpinnerLogsWithoutTerminal(t *testing.T) {
	th := asciiTheme(false)
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Spinner.Running = `{{ boom }}`
	th.Templates.Spinner.Failed = `{{ boom }}`
	terst.UseTheme(t, th)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	var out bytes.Buffer
	s, err := NewSpinners(t.Context(), WithOutput(&out))
	assert.NoError(t, err)
	sp, err := s.Add(t.Context(), WithPrefixf("job"))
	assert.NoError(t, err)
	sp.Update("working")
	sp.Fail(errors.New("lost"))
	assert.NoError(t, sp.Close())
	s.Close()
	assert.Equal(t, ""+
		"level=INFO msg=\"spinner started\" spinner=0 prefix=job status=started\n"+
		"level=INFO msg=working spinner=0 prefix=job status=running\n"+
		"level=ERROR msg=lost spinner=0 prefix=job status=failed\n", logs.String())
	assert.Equal(t, 0, out.Len())
}
