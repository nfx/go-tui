// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"github.com/nfx/go-tui/internal/color"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/term"
)

// tio owns package-default terminal IO and lazily upgrades stderr to a shared
// arbiter when the configured output is an interactive TTY.
type tio struct {
	reader   io.Reader
	writer   io.Writer
	external io.Writer
	mu       sync.RWMutex
	arbiter  *chanIO
	cancel   context.CancelCauseFunc
	env      outputEnv
	// bg caches appearance, including timeouts, until setIO replaces the route.
	bg    *color.Background
	bgGen uint64
	// bgMu serializes probes; acquire before mu and release mu before probing.
	bgMu sync.Mutex
	// theme is the installed snapshot, set only on defaultIO. Renderers load
	// it without locking.
	theme atomic.Pointer[compiledTheme]
	// themeMu serializes installations, so that an edit of the installed
	// theme cannot lose a concurrent one. It is never held while rendering.
	themeMu sync.Mutex
}

func (t *tio) currentTheme() *compiledTheme { return t.theme.Load() }

// installTheme replaces the configuration and custom helpers only after validation.
func (t *tio) installTheme(th Theme) error {
	t.themeMu.Lock()
	defer t.themeMu.Unlock()
	return t.storeThemeLocked(th)
}

// updateTheme serializes edits to the installed configuration to prevent lost updates.
func (t *tio) updateTheme(edit func(*Theme)) error {
	t.themeMu.Lock()
	defer t.themeMu.Unlock()
	th := t.currentTheme().editable()
	edit(&th)
	return t.storeThemeLocked(th)
}

// storeThemeLocked publishes a validated theme while the caller holds themeMu.
func (t *tio) storeThemeLocked(th Theme) error {
	c, err := t.compileTheme(th)
	if err != nil {
		return err
	}
	t.theme.Store(c)
	return nil
}

// compileTheme compiles th for the terminal of the process: without the
// Unicode symbols in the environment, the presets become ASCII.
func (t *tio) compileTheme(th Theme) (*compiledTheme, error) {
	c, err := th.compile()
	if err != nil || t.env.hasUnicode() {
		return c, err
	}
	c.source.Symbols = c.source.Symbols.ascii()
	return c, nil
}

var defaultIO = newDefaultIO()

// newDefaultIO initializes standard streams and the built-in theme.
func newDefaultIO() *tio {
	t := &tio{
		reader: os.Stdin,
		writer: os.Stderr,
		env:    readOutputEnv(os.LookupEnv),
	}
	c, err := t.compileTheme(NewTheme())
	if err != nil {
		panic(err) // the preset is valid
	}
	t.theme.Store(c)
	return t
}

// defaultStreams returns the default reader and writer as one snapshot, so a
// concurrent [SetDefaultIO] cannot pair the old reader with the new writer.
func defaultStreams(ctx context.Context) (io.Reader, io.Writer) {
	return defaultIO.streams(ctx)
}

// Flush drains all buffered terminal output without shutting down the arbiter.
func (t *tio) Flush() error {
	t.mu.RLock()
	arbiter := t.arbiter
	t.mu.RUnlock()
	if arbiter == nil {
		return nil
	}
	return arbiter.Flush()
}

// Close cancels the terminal arbiter and blocks until forwardTo has drained
// all buffered output to the underlying writer. Safe to call more than once.
func (t *tio) Close() error {
	t.mu.Lock()
	arbiter := t.arbiter
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel(nil)
	}
	if arbiter == nil {
		return nil
	}
	<-arbiter.done
	return arbiter.err
}

// profileFor resolves the color profile for one destination writer.
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
func (t *tio) profileFor(w io.Writer) color.Profile {
	e := t.env
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
	case *chanIO, *viewport, *Tui:
		w = t.rawOutput()
	}
	terminal := t.isTerminalWriter(w)
	switch {
	case e.noColor && terminal:
		return color.ProfileAttrs
	case e.noColor:
		return color.ProfilePlain
	case e.cliColorForce:
		return e.depth()
	case !terminal, e.term == "dumb", !consoleVT(w):
		return color.ProfilePlain
	}
	return e.depth()
}

func (t *tio) input() io.Reader {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.reader
}

func (t *tio) rawOutput() io.Writer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.writer
}

// streams takes the exclusive lock because [tio.outputLocked] may start the arbiter.
func (t *tio) streams(ctx context.Context) (io.Reader, io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reader, t.outputLocked(ctx)
}

// outputLocked resolves the effective writer,
// upgrading to arbiter for TTY outputs.
func (t *tio) outputLocked(ctx context.Context) io.Writer {
	if cio, ok := t.writer.(*chanIO); ok {
		return cio
	}
	if !t.interactiveWriter(t.writer) {
		return t.writer
	}
	cio := t.ensureArbiterLocked(ctx)
	if cio == nil {
		return t.writer
	}
	return cio
}

// background shares detection for the current route, caching only uncancelled,
// still-current results. Other routes bypass the cache. Without a reply,
// detection falls back to hint.
func (t *tio) background(ctx context.Context, tt *termIO, hint color.Background) color.Background {
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

// bindTheme binds c to out, the destination of one operation. Only a prompt
// holding the terminal lease passes tt, and may then probe the background
// before it starts reading keys; other operations reuse a completed
// detection of the route they draw on, or estimate it from the environment.
// A forced light variant and a destination without color detect nothing.
func (t *tio) bindTheme(ctx context.Context, c *compiledTheme, out io.Writer, tt *termIO) binding {
	p := t.profileFor(out)
	var bg color.Background
	switch {
	case c.source.Light, p < color.ProfileANSI16:
	case tt != nil:
		bg = t.background(ctx, tt, t.env.bgHint)
	default:
		bg = t.routeBackground(out)
	}
	return c.bind(p, bg)
}

// routeBackground is the cached detection of the current route when out
// draws on it, else the environment estimate. It never waits for a probe
// in progress, so output-only work cannot block on a prompt.
func (t *tio) routeBackground(out io.Writer) color.Background {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.bg == nil || !t.drawsOnRouteLocked(out) {
		return t.env.bgHint
	}
	return *t.bg
}

// drawsOnRouteLocked reports, with mu held, whether out writes to the
// terminal of the current route: its writer, the arbiter on it, or [Stderr].
func (t *tio) drawsOnRouteLocked(out io.Writer) bool {
	if w, ok := out.(*terminalStderr); ok {
		return w.parent == t
	}
	return t.sameStream(out, t.writer) || t.sameStream(out, t.arbiter)
}

// routesLocked checks route ownership with mu held.
func (t *tio) routesLocked(tt *termIO) bool {
	return t.sameStream(tt.in, t.reader) &&
		(t.sameStream(tt.out, t.writer) || t.sameStream(tt.out, t.arbiter))
}

// sameStream compares stream identity without panicking on uncomparable values.
func (t *tio) sameStream(a, b any) bool {
	if a == nil || b == nil || !reflect.TypeOf(a).Comparable() || !reflect.TypeOf(b).Comparable() {
		return false
	}
	return a == b
}

// stderrWriter returns a writer for external
// stderr, routed through the arbiter on TTYs.
func (t *tio) stderrWriter() io.Writer {
	t.mu.Lock()
	defer t.mu.Unlock()
	cio, ok := t.writer.(*chanIO)
	if ok {
		return cio
	}
	if !t.interactiveWriter(t.writer) {
		return t.writer
	}
	cio = t.ensureArbiterLocked(context.Background())
	if cio == nil {
		return t.writer
	}
	if t.external == nil {
		t.external = &terminalStderr{parent: t}
	}
	return t.external
}

// writeExternal sends output through the arbiter or
// falls back to the raw writer.
func (t *tio) writeExternal(p []byte) (int, error) {
	t.mu.Lock()
	cio := t.ensureArbiterLocked(context.Background())
	raw := t.writer
	t.mu.Unlock()
	if cio != nil {
		return cio.Write(p)
	}
	if raw == nil {
		return 0, io.ErrClosedPipe
	}
	return raw.Write(p)
}

// setIO replaces the default reader and/or writer,
// resetting the arbiter on writer change.
func (t *tio) setIO(in io.Reader, out io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if in != nil {
		t.reader = in
		t.bg, t.bgGen = nil, t.bgGen+1
	}
	if out == nil {
		return
	}
	t.bg, t.bgGen = nil, t.bgGen+1
	t.writer = t.unwrapWriterLocked(out)
	t.resetArbiterLocked()
}

// unwrapWriterLocked resolves self-referential writers
// to avoid circular write chains.
func (t *tio) unwrapWriterLocked(out io.Writer) io.Writer {
	switch w := out.(type) {
	case *terminalStderr:
		if w.parent == t {
			return t.writer
		}
		return w.parent.rawOutput()
	case *chanIO:
		if w == t.arbiter {
			return t.writer
		}
	}
	return out
}

// resetArbiterLocked tears down
// the arbiter goroutine and clears
// related state.
func (t *tio) resetArbiterLocked() {
	if t.cancel != nil {
		t.cancel(nil)
	}
	t.cancel = nil
	t.arbiter = nil
	t.external = nil
}

// ensureArbiterLocked lazily creates
// the channel-based IO arbiter for TTY outputs.
func (t *tio) ensureArbiterLocked(ctx context.Context) *chanIO {
	if cio, ok := t.writer.(*chanIO); ok {
		return cio
	}
	if t.arbiter != nil {
		return t.arbiter
	}
	if !t.interactiveWriter(t.writer) {
		return nil
	}
	// the arbiter is shared by the whole process, so it must outlive the first caller's cancellation.
	arbiterCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	cio, err := startIO(arbiterCtx, t.writer)
	if err != nil {
		cancel(err)
		return nil
	}
	t.arbiter = cio
	t.cancel = cancel
	return cio
}

var termGetSize = term.GetSize
var terminalWriterChecker = func(fd int) bool {
	return term.IsTerminal(fd)
}

// interactiveWriter reports whether w is a terminal to redraw on: a terminal
// outside of CI. Color depth still follows [tio.isTerminalWriter].
func (t *tio) interactiveWriter(w io.Writer) bool {
	return !t.env.ci && t.isTerminalWriter(w)
}

func (*tio) isTerminalWriter(w io.Writer) bool {
	fdw, ok := w.(descriptor)
	if !ok {
		return false
	}
	return terminalWriterChecker(int(fdw.Fd()))
}
