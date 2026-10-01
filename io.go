// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/term"
)

type bbuf []byte

func (b *bbuf) String() string {
	return string(*b)
}

func (b *bbuf) Write(p []byte) (n int, err error) {
	*b = append(*b, p...)
	return len(p), nil
}

// tio owns package-default terminal IO and lazily upgrades stderr to a shared
// arbiter when the configured output is an interactive TTY.
type tio struct {
	reader   io.Reader
	writer   io.Writer
	external io.Writer
	mu       sync.RWMutex
	arbiter  *chanIO
	cancel   context.CancelFunc
}

type terminalStderr struct {
	parent *tio
}

func (w *terminalStderr) Write(p []byte) (n int, err error) {
	if w == nil || w.parent == nil {
		return 0, io.ErrClosedPipe
	}
	return w.parent.writeExternal(p)
}

// Fd returns the file descriptor of
// the underlying terminal writer.
func (w *terminalStderr) Fd() uintptr {
	if w == nil || w.parent == nil {
		return 0
	}
	fdw, ok := w.parent.rawOutput().(descriptor)
	if !ok {
		return 0
	}
	return fdw.Fd()
}

var defaultIO = &tio{
	reader: os.Stdin,
	writer: os.Stderr,
}

// Stderr returns the package-default append-only terminal writer.
//
// On interactive TTYs this routes writes through go-tui's shared terminal
// arbiter so prompts and external output are ordered by one renderer. On
// non-TTY outputs it returns the configured raw writer directly.
func Stderr() io.Writer {
	return defaultIO.stderrWriter()
}

// SetDefaultIO overrides package-wide default input/output used by widgets that
// were not explicitly configured with [WithInput] or [WithOutput].
//
// Deprecated: prefer the package defaults plus [Stderr] for normal CLI + TUI
// integration. This remains for tests and explicit advanced overrides.
func SetDefaultIO(in io.Reader, out io.Writer) {
	defaultIO.setIO(in, out)
}

func defaultInput() io.Reader {
	return defaultIO.input()
}

func defaultOutput() io.Writer {
	return defaultIO.output()
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

func (t *tio) output() io.Writer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outputLocked()
}

// outputLocked resolves the effective writer,
// upgrading to arbiter for TTY outputs.
func (t *tio) outputLocked() io.Writer {
	if cio, ok := t.writer.(*chanIO); ok {
		return cio
	}
	if !t.isTerminalWriter(t.writer) {
		return t.writer
	}
	cio := t.ensureArbiterLocked()
	if cio == nil {
		return t.writer
	}
	return cio
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
	if !t.isTerminalWriter(t.writer) {
		return t.writer
	}
	cio = t.ensureArbiterLocked()
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
	cio := t.ensureArbiterLocked()
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
	}
	if out == nil {
		return
	}
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
		t.cancel()
	}
	t.cancel = nil
	t.arbiter = nil
	t.external = nil
}

// ensureArbiterLocked lazily creates
// the channel-based IO arbiter for TTY outputs.
func (t *tio) ensureArbiterLocked() *chanIO {
	if cio, ok := t.writer.(*chanIO); ok {
		return cio
	}
	if t.arbiter != nil {
		return t.arbiter
	}
	if !t.isTerminalWriter(t.writer) {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cio, err := startIO(ctx, t.writer)
	if err != nil {
		cancel()
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

func (*tio) isTerminalWriter(w io.Writer) bool {
	fdw, ok := w.(descriptor)
	if !ok {
		return false
	}
	return terminalWriterChecker(int(fdw.Fd()))
}

// startIO boots the arbiter goroutines that multiplex viewport
// and external writes.
func startIO(ctx context.Context, out io.Writer) (*chanIO, error) {
	fdw, ok := out.(descriptor)
	if !ok {
		return nil, fmt.Errorf("stderr: %w", ErrNoTTY)
	}
	fd := int(fdw.Fd())
	w, h, err := termGetSize(fd)
	if err != nil {
		return nil, fmt.Errorf("get size: %w", err)
	}
	cio := newUnstartedIO(ctx, w, h, fd)
	go cio.handleViewports(ctx)
	go cio.forwardTo(ctx, out)
	return cio, nil
}

// newUnstartedIO allocates a chanIO with an initial viewport but
// no render goroutines.
func newUnstartedIO(ctx context.Context, width, height, fd int) *chanIO {
	cio := &chanIO{
		ctx:    ctx,
		In:     make(chan string),
		Out:    make(chan string, 1024),
		vreply: make(chan chan *viewport),
		notify: make(chan viewportChanged, 1024), // buffered to avoid blocking
		width:  width,
		height: height,
		fd:     fd,
	}
	cio.head = initViewport(ctx, cio.notify, cio.width, cio.height)
	cio.tail = cio.head
	return cio
}

// implements [io.ReadWriter].
type chanIO struct {
	In  chan string
	Out chan string

	ctx context.Context

	width, height int
	head, tail    *viewport
	vreply        chan chan *viewport
	notify        chan viewportChanged
	fd            int // terminal fd for resize refresh, 0 if unavailable
	rendered      []int
}

// Deprecated: use [Stderr].
func NewIO(ctx context.Context) (*chanIO, error) {
	return startIO(ctx, os.Stderr)
}

// Read blocks until the next input string arrives
// on the In channel.
func (i *chanIO) Read(p []byte) (n int, err error) {
	select {
	case <-i.ctx.Done():
		return 0, io.EOF
	case res, ok := <-i.In:
		if !ok {
			return 0, io.EOF
		}
		copy(p, res)
		return len(res), nil
	}
}

// Write sends output bytes to the Out channel for
// the render loop to flush.
func (i *chanIO) Write(p []byte) (n int, err error) {
	select { // don't send on a closed channel
	case <-i.ctx.Done():
		return 0, io.EOF
	default:
	}
	select {
	case <-i.ctx.Done():
		return 0, io.EOF
	case i.Out <- string(p):
		return len(p), nil
	}
}

// pushViewport requests a new managed viewport from
// the arbiter's handleViewports loop.
func (i *chanIO) pushViewport() (*viewport, error) {
	select {
	case <-i.ctx.Done():
		return nil, i.ctx.Err()
	default:
	}
	added := make(chan *viewport)
	defer close(added)
	select {
	case <-i.ctx.Done():
		return nil, i.ctx.Err()
	case i.vreply <- added:
		select {
		case <-i.ctx.Done():
			return nil, i.ctx.Err()
		default:
		}
		select {
		case <-i.ctx.Done():
			return nil, i.ctx.Err()
		case vp := <-added:
			return vp, nil
		}
	}
}

// refreshSize re-queries the terminal size for the shared arbiter.
func (i *chanIO) refreshSize() {
	if i.fd < 1 {
		return
	}
	w, h, err := termGetSize(i.fd)
	if err != nil {
		return
	}
	i.width = w
	i.height = h
}

// handleViewports listens for viewport push requests
// and inserts them into the managed chain.
func (i *chanIO) handleViewports(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case reply := <-i.vreply:
			i.refreshSize()
			vp := initViewport(ctx, i.notify, i.width, i.height)
			vp.fixedHeight = true
			i.insertManagedViewport(vp)
			select {
			case <-ctx.Done():
				return
			case reply <- vp:
			}
		}
	}
}

// insertManagedViewport keeps background overlays
// above later interactive prompts.
func (i *chanIO) insertManagedViewport(vp *viewport) {
	if i.head == nil || !i.head.fixedHeight {
		vp.next = i.head
		i.head = vp
		if i.tail == nil {
			i.tail = vp
		}
		return
	}
	curr := i.head
	for curr.next != nil && curr.next.fixedHeight {
		curr = curr.next
	}
	vp.next = curr.next
	curr.next = vp
	if vp.next == nil {
		i.tail = vp
	}
}

// forwardTo is the main render loop that flushes overlay changes and external writes to the terminal.
func (i *chanIO) forwardTo(ctx context.Context, w io.Writer) {
	var prevH int
	var pending bytes.Buffer
	var nl externalNewlineState
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-i.notify:
			prevH = i.handleOverlayChange(w, prevH, &pending, &nl)
			if ev.done != nil {
				close(ev.done)
			}
		case chunk := <-i.Out:
			prevH = i.handleExternalWrite(w, prevH, &pending, &nl, chunk)
		}
	}
}

// handleOverlayChange redraws managed viewports and flushes pending output when overlays clear.
func (i *chanIO) handleOverlayChange(
	w io.Writer,
	prevH int,
	pending *bytes.Buffer,
	nl *externalNewlineState,
) int {
	i.refreshSize()
	currH, err := i.redrawManaged(w, prevH)
	if err != nil {
		return prevH
	}
	if currH != 0 || pending.Len() == 0 {
		return currH
	}
	if _, err = w.Write(nl.normalize(pending.Bytes())); err == nil {
		pending.Reset()
	}
	return currH
}

// handleExternalWrite interleaves external output with managed viewport redraws.
func (i *chanIO) handleExternalWrite(
	w io.Writer,
	prevH int,
	pending *bytes.Buffer,
	nl *externalNewlineState,
	chunk string,
) int {
	if prevH == 0 {
		return i.flushWithoutOverlay(w, pending, nl, chunk, prevH)
	}
	i.refreshSize()
	pending.WriteString(chunk)
	flush, rest := i.splitCompletedLines(pending.Bytes())
	pending.Reset()
	_, _ = pending.Write(rest)

	var buf bytes.Buffer
	i.clearManaged(&buf)
	if len(flush) > 0 {
		_, _ = buf.Write(nl.normalize(flush))
	}
	currH, err := i.writeManaged(&buf)
	if err != nil {
		return prevH
	}
	if buf.Len() == 0 {
		return currH
	}
	if _, err = w.Write(buf.Bytes()); err != nil {
		return prevH
	}
	return currH
}

// flushWithoutOverlay writes external output directly when no managed viewports are active.
func (i *chanIO) flushWithoutOverlay(
	w io.Writer,
	pending *bytes.Buffer,
	nl *externalNewlineState,
	chunk string,
	prevH int,
) int {
	if pending.Len() > 0 {
		pending.WriteString(chunk)
		if _, err := w.Write(nl.normalize(pending.Bytes())); err == nil {
			pending.Reset()
		}
		return prevH
	}
	if _, err := w.Write(nl.normalizeString(chunk)); err != nil {
		return prevH
	}
	return prevH
}

type externalNewlineState struct {
	prevCR bool
}

// normalize rewrites lone LF bytes to CRLF for raw terminal output.
func (s *externalNewlineState) normalize(p []byte) []byte {
	if len(p) == 0 {
		return nil
	}
	out := make([]byte, 0, len(p))
	for _, b := range p {
		if b == '\n' && !s.prevCR {
			out = append(out, '\r')
		}
		out = append(out, b)
		s.prevCR = b == '\r'
	}
	return out
}

func (s *externalNewlineState) normalizeString(x string) []byte {
	return s.normalize([]byte(x))
}

// splitCompletedLines separates fully newline-terminated lines from
// a trailing partial line.
func (*chanIO) splitCompletedLines(p []byte) (flush []byte, rest []byte) {
	idx := bytes.LastIndexByte(p, '\n')
	if idx < 0 {
		return nil, append([]byte(nil), p...)
	}
	flush = append([]byte(nil), p[:idx+1]...)
	rest = append([]byte(nil), p[idx+1:]...)
	return flush, rest
}

// clearManaged emits ANSI escape sequences to erase
// previously rendered managed lines.
func (i *chanIO) clearManaged(buf *bytes.Buffer) {
	lines := i.wrappedRows(i.rendered, i.width)
	if lines <= 0 {
		return
	}
	for i := range lines {
		fmt.Fprint(buf, "\r")
		fmt.Fprint(buf, "\x1b[K")
		if i < lines-1 {
			fmt.Fprint(buf, "\x1b[1A")
		}
	}
}

// redrawManaged clears old managed output and rewrites all active
// managed viewports.
func (i *chanIO) redrawManaged(w io.Writer, prevH int) (int, error) {
	var buf bytes.Buffer
	i.clearManaged(&buf)
	lines, err := i.writeManaged(&buf)
	if err != nil {
		return prevH, err
	}
	if buf.Len() == 0 {
		return lines, nil
	}
	_, err = w.Write(buf.Bytes())
	return lines, err
}

// writeManaged renders all managed viewports into
// the writer within the terminal height budget.
func (i *chanIO) writeManaged(w io.Writer) (int, error) {
	i.rendered = i.rendered[:0]
	if i.head == nil {
		return 0, nil
	}
	var totalLines int
	curr := i.head
	budget := i.height
	for curr != nil {
		if !i.isManagedViewport(curr) {
			break
		}
		res, err := i.writeViewport(curr, w)
		if err != nil {
			return totalLines, err
		}
		totalLines += res.lines
		i.rendered = append(i.rendered, res.widths...)
		budget -= res.lines
		if budget <= 0 {
			break
		}
		curr = i.nextManagedViewport(curr, budget)
	}
	i.trimTrailingNewline(w)
	return totalLines, nil
}

func (i *chanIO) wrappedRows(widths []int, termWidth int) int {
	if len(widths) == 0 {
		return 0
	}
	if termWidth <= 0 {
		return len(widths)
	}
	rows := 0
	for _, lineWidth := range widths {
		if lineWidth <= 0 {
			rows++
			continue
		}
		rows += (lineWidth + termWidth - 1) / termWidth
	}
	return rows
}

func (i *chanIO) isManagedViewport(curr *viewport) bool {
	return curr == i.head || curr.fixedHeight
}

func (*chanIO) nextManagedViewport(curr *viewport, budget int) *viewport {
	curr = curr.next
	if curr != nil {
		curr.height = budget
	}
	return curr
}

// trimTrailingNewline removes a trailing newline
// from the buffer to avoid extra blank lines.
func (*chanIO) trimTrailingNewline(w io.Writer) {
	buf, ok := w.(*bytes.Buffer)
	if !ok {
		return
	}
	x := buf.Bytes()
	if len(x) > 0 && x[len(x)-1] == '\n' {
		buf.Truncate(len(x) - 1)
	}
}

// writeViewport sends a writeTo request to a viewport and waits for its rendered response.
func (*chanIO) writeViewport(curr *viewport, w io.Writer) (writeToResponse, error) {
	respond := make(chan writeToResponse)
	select {
	case <-curr.ctx.Done():
		return writeToResponse{}, io.EOF
	case curr.writeTos <- &writeTo{Writer: w, res: respond}:
	}
	select {
	case <-curr.ctx.Done():
		return writeToResponse{}, io.EOF
	case res := <-respond:
		close(respond)
		if res.err != nil {
			return writeToResponse{}, res.err
		}
		return res, nil
	}
}

func newWriteC(ctx context.Context) *writeC {
	return &writeC{
		Context: ctx,
		C:       make(chan string, 128),
	}
}

type writeC struct {
	context.Context
	C chan string
}

func (x *writeC) Write(p []byte) (n int, err error) {
	select {
	case <-x.Done():
		return 0, io.EOF
	case x.C <- string(p):
		return len(p), nil
	}
}
