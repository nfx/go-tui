// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"context"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/internal/render"
	"github.com/nfx/go-tui/theme"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/term"
)

// Terminal owns package-default terminal IO and lazily upgrades stderr to a shared
// arbiter when the configured output is an interactive TTY.
type Terminal struct {
	Reader   io.Reader
	Writer   io.Writer
	external io.Writer
	mu       sync.RWMutex
	Arbiter  *ChanIO
	Cancel   context.CancelCauseFunc
	Env      outputEnv
	// bg caches appearance, including timeouts, until setIO replaces the route.
	bg    *color.Background
	bgGen uint64
	// bgMu serializes probes; acquire before mu and release mu before probing.
	bgMu sync.Mutex
	// Theme is the installed snapshot, set only on defaultIO. Renderers load
	// it without locking.
	Theme atomic.Pointer[render.Compiled]
	// themeMu serializes installations, so that an edit of the installed
	// theme cannot lose a concurrent one. It is never held while rendering.
	themeMu sync.Mutex
}

func (t *Terminal) CurrentTheme() *render.Compiled { return t.Theme.Load() }

// InstallTheme replaces the configuration and custom helpers only after validation.
func (t *Terminal) InstallTheme(th theme.Theme) error {
	t.themeMu.Lock()
	defer t.themeMu.Unlock()
	return t.storeThemeLocked(th)
}

// UpdateTheme serializes edits to the installed configuration to prevent lost updates.
func (t *Terminal) UpdateTheme(edit func(*theme.Theme)) error {
	t.themeMu.Lock()
	defer t.themeMu.Unlock()
	th := t.CurrentTheme().Editable()
	edit(&th)
	return t.storeThemeLocked(th)
}

// storeThemeLocked publishes a validated theme while the caller holds themeMu.
func (t *Terminal) storeThemeLocked(th theme.Theme) error {
	c, err := t.compileTheme(th)
	if err != nil {
		return err
	}
	t.Theme.Store(c)
	return nil
}

// compileTheme compiles th for the terminal of the process: without the
// Unicode symbols in the environment, the presets become ASCII.
func (t *Terminal) compileTheme(th theme.Theme) (*render.Compiled, error) {
	c, err := render.Compile(th)
	if err != nil || t.Env.hasUnicode() {
		return c, err
	}
	c.Source.Symbols = c.Source.Symbols.ASCII()
	return c, nil
}

var Default = newDefaultIO()

// newDefaultIO initializes standard streams and the built-in theme.
func newDefaultIO() *Terminal {
	t := &Terminal{
		Reader: os.Stdin,
		Writer: os.Stderr,
		Env:    ReadOutputEnv(os.LookupEnv),
	}
	c, err := t.compileTheme(theme.New())
	if err != nil {
		panic(err) // the preset is valid
	}
	t.Theme.Store(c)
	return t
}

// DefaultStreams returns the default reader and writer as one snapshot, so a
// concurrent [SetDefaultIO] cannot pair the old reader with the new writer.
func DefaultStreams(ctx context.Context) (io.Reader, io.Writer) {
	return Default.streams(ctx)
}

// Flush drains all buffered terminal output without shutting down the arbiter.
func (t *Terminal) Flush() error {
	t.mu.RLock()
	arbiter := t.Arbiter
	t.mu.RUnlock()
	if arbiter == nil {
		return nil
	}
	return arbiter.Flush()
}

// Close cancels the terminal arbiter and blocks until forwardTo has drained
// all buffered output to the underlying writer. Safe to call more than once.
func (t *Terminal) Close() error {
	t.mu.Lock()
	arbiter := t.Arbiter
	cancel := t.Cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel(nil)
	}
	if arbiter == nil {
		return nil
	}
	<-arbiter.Done
	return arbiter.err
}

// ProfileFor resolves the color profile for one destination writer.
// Managed writers (the arbiter, viewports, [Tui]) draw on t's raw output.
// The first matching rule wins:
//
//  1. FORCE_COLOR: 0/false is plain, 2 is ansi256, 3 is truecolor, others ansi16.
//  2. NO_COLOR, or CI without CLICOLOR_FORCE: attrs on a terminal, plain
//     elsewhere.
//  3. CLICOLOR_FORCE: color on any writer, depth from terminal hints.
//  4. Non-terminal writers are plain.
//  5. Terminal writers: TERM=dumb is plain, otherwise depth from
//     COLORTERM, TERM and WT_SESSION.
//
// A forced profile never enables prompts, raw mode or redraw.
func (t *Terminal) ProfileFor(w io.Writer) color.Profile {
	e := t.Env
	if e.forceColor != nil {
		switch strings.ToLower(*e.forceColor) {
		case "0", "false":
			return color.ProfilePlain
		case "2":
			return color.ProfileANSI256
		case "3":
			return color.ProfileTrueColor
		}
		return color.ProfileANSI16
	}
	switch w.(type) {
	case *ChanIO, *Viewport, *Tui:
		w = t.RawOutput()
	}
	terminal := t.isTerminalWriter(w)
	switch {
	case e.noColor && terminal:
		return color.ProfileAttrs
	case e.noColor:
		return color.ProfilePlain
	case e.cliColorForce:
		return e.depth()
	case !terminal, e.term == "dumb", !ConsoleVT(w):
		return color.ProfilePlain
	}
	return e.depth()
}

func (t *Terminal) Input() io.Reader {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Reader
}

func (t *Terminal) RawOutput() io.Writer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Writer
}

// streams takes the exclusive lock because [tio.outputLocked] may start the arbiter.
func (t *Terminal) streams(ctx context.Context) (io.Reader, io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Reader, t.outputLocked(ctx)
}

// outputLocked resolves the effective writer,
// upgrading to arbiter for TTY outputs.
func (t *Terminal) outputLocked(ctx context.Context) io.Writer {
	if cio, ok := t.Writer.(*ChanIO); ok {
		return cio
	}
	if !t.interactiveWriter(t.Writer) {
		return t.Writer
	}
	cio := t.ensureArbiterLocked(ctx)
	if cio == nil {
		return t.Writer
	}
	return cio
}

// background shares detection for the current route, caching only uncancelled,
// still-current results. Other routes bypass the cache. Without a reply,
// detection falls back to hint.
func (t *Terminal) background(ctx context.Context, tt *IO, hint color.Background) color.Background {
	t.bgMu.Lock()
	defer t.bgMu.Unlock()
	t.mu.RLock()
	bg, gen, current := t.bg, t.bgGen, t.routesLocked(tt)
	t.mu.RUnlock()
	if !current {
		return tt.detectBackground(ctx, hint)
	}
	if bg != nil {
		return *bg
	}
	b := tt.detectBackground(ctx, hint)
	if ctx.Err() == nil {
		t.mu.Lock()
		if t.bgGen == gen {
			t.bg = &b
		}
		t.mu.Unlock()
	}
	return b
}

// BindTheme binds c to out, the destination of one operation. Only a prompt
// holding the terminal lease passes tt, and may then probe the background
// before it starts reading keys; other operations reuse a completed
// detection of the route they draw on, or estimate it from the environment.
// A forced light variant and a destination without color detect nothing.
func (t *Terminal) BindTheme(ctx context.Context, c *render.Compiled, out io.Writer, tt *IO) render.Binding {
	p := t.ProfileFor(out)
	var bg color.Background
	switch {
	case c.Source.Light, p < color.ProfileANSI16:
	case tt != nil:
		bg = t.background(ctx, tt, t.Env.bgHint)
	default:
		bg = t.routeBackground(out)
	}
	return c.Bind(p, bg)
}

// routeBackground is the cached detection of the current route when out
// draws on it, else the environment estimate. It never waits for a probe
// in progress, so output-only work cannot block on a prompt.
func (t *Terminal) routeBackground(out io.Writer) color.Background {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.bg == nil || !t.drawsOnRouteLocked(out) {
		return t.Env.bgHint
	}
	return *t.bg
}

// drawsOnRouteLocked reports, with mu held, whether out writes to the
// terminal of the current route: its writer, the arbiter on it, or [Stderr].
func (t *Terminal) drawsOnRouteLocked(out io.Writer) bool {
	if w, ok := out.(*TerminalStderr); ok {
		return w.Parent == t
	}
	return t.sameStream(out, t.Writer) || t.sameStream(out, t.Arbiter)
}

// routesLocked checks route ownership with mu held.
func (t *Terminal) routesLocked(tt *IO) bool {
	return t.sameStream(tt.In, t.Reader) &&
		(t.sameStream(tt.Out, t.Writer) || t.sameStream(tt.Out, t.Arbiter))
}

// sameStream compares stream identity without panicking on uncomparable values.
func (t *Terminal) sameStream(a, b any) bool {
	if a == nil || b == nil || !reflect.TypeOf(a).Comparable() || !reflect.TypeOf(b).Comparable() {
		return false
	}
	return a == b
}

// StderrWriter returns a writer for external
// stderr, routed through the arbiter on TTYs.
func (t *Terminal) StderrWriter() io.Writer {
	t.mu.Lock()
	defer t.mu.Unlock()
	cio, ok := t.Writer.(*ChanIO)
	if ok {
		return cio
	}
	if !t.interactiveWriter(t.Writer) {
		return t.Writer
	}
	cio = t.ensureArbiterLocked(context.Background())
	if cio == nil {
		return t.Writer
	}
	if t.external == nil {
		t.external = &TerminalStderr{Parent: t}
	}
	return t.external
}

// writeExternal sends output through the arbiter or
// falls back to the raw writer.
func (t *Terminal) writeExternal(p []byte) (int, error) {
	t.mu.Lock()
	cio := t.ensureArbiterLocked(context.Background())
	raw := t.Writer
	t.mu.Unlock()
	if cio != nil {
		return cio.Write(p)
	}
	if raw == nil {
		return 0, io.ErrClosedPipe
	}
	return raw.Write(p)
}

// SetIO replaces the default reader and/or writer,
// resetting the arbiter on writer change.
func (t *Terminal) SetIO(in io.Reader, out io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if in != nil {
		t.Reader = in
		t.bg, t.bgGen = nil, t.bgGen+1
	}
	if out == nil {
		return
	}
	t.bg, t.bgGen = nil, t.bgGen+1
	t.Writer = t.unwrapWriterLocked(out)
	t.resetArbiterLocked()
}

// unwrapWriterLocked resolves self-referential writers
// to avoid circular write chains.
func (t *Terminal) unwrapWriterLocked(out io.Writer) io.Writer {
	switch w := out.(type) {
	case *TerminalStderr:
		if w.Parent == t {
			return t.Writer
		}
		return w.Parent.RawOutput()
	case *ChanIO:
		if w == t.Arbiter {
			return t.Writer
		}
	}
	return out
}

// resetArbiterLocked tears down
// the arbiter goroutine and clears
// related state.
func (t *Terminal) resetArbiterLocked() {
	if t.Cancel != nil {
		t.Cancel(nil)
	}
	t.Cancel = nil
	t.Arbiter = nil
	t.external = nil
}

// ensureArbiterLocked lazily creates
// the channel-based IO arbiter for TTY outputs.
func (t *Terminal) ensureArbiterLocked(ctx context.Context) *ChanIO {
	if cio, ok := t.Writer.(*ChanIO); ok {
		return cio
	}
	if t.Arbiter != nil {
		return t.Arbiter
	}
	if !t.interactiveWriter(t.Writer) {
		return nil
	}
	// the arbiter is shared by the whole process, so it must outlive the first caller's cancellation.
	arbiterCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	cio, err := StartIO(arbiterCtx, t.Writer)
	if err != nil {
		cancel(err)
		return nil
	}
	t.Arbiter = cio
	t.Cancel = cancel
	return cio
}

var GetSize = term.GetSize
var WriterChecker = func(fd int) bool {
	return term.IsTerminal(fd)
}

// interactiveWriter reports whether w is a terminal to redraw on: a terminal
// outside of CI. Color depth still follows [tio.isTerminalWriter].
func (t *Terminal) interactiveWriter(w io.Writer) bool {
	return !t.Env.Ci && t.isTerminalWriter(w)
}

func (*Terminal) isTerminalWriter(w io.Writer) bool {
	fdw, ok := w.(Descriptor)
	if !ok {
		return false
	}
	return WriterChecker(int(fdw.Fd()))
}
