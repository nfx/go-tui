// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package term

import (
	"bytes"
	"context"
	"github.com/nfx/go-tui/internal/errs"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/nfx/go-tui/internal/assert"
)

func TestScanControlFrame(t *testing.T) {
	invalid := controlFrame{isInvalid: true}
	for name, tc := range map[string]struct {
		in   string
		want controlFrame
	}{
		"bel":         {"\x1b]11;rgb:ffff/0000/8080\x07", controlFrame{size: 24, isComplete: true, reply: controlReply{11, "rgb:ffff/0000/8080"}}},
		"st":          {"\x1b]11;rgb:ff/00/80\x1b\\", controlFrame{size: 19, isComplete: true, reply: controlReply{11, "rgb:ff/00/80"}}},
		"trailing":    {"\x1b]11;x\x07a", controlFrame{size: 7, isComplete: true, reply: controlReply{11, "x"}}},
		"empty data":  {"\x1b]4;\x07", controlFrame{size: 5, isComplete: true, reply: controlReply{4, ""}}},
		"alt bracket": {"\x1b]", controlFrame{size: 2, isIncomplete: true}},
		"code only":   {"\x1b]11", controlFrame{size: 4, isIncomplete: true}},
		"no end":      {"\x1b]11;rgb:", controlFrame{size: 9, isIncomplete: true}},
		"lone esc":    {"\x1b]11;x\x1b", controlFrame{size: 7, isIncomplete: true}},
		"no code":     {"\x1b];x\x07", invalid},
		"letter code": {"\x1b]a;x\x07", invalid},
		"long code":   {"\x1b]1111;x\x07", invalid},
		"control":     {"\x1b]11;a\rb\x07", invalid},
		"bad esc":     {"\x1b]11;a\x1b[A\x07", invalid},
		"too long":    {"\x1b]11;" + strings.Repeat("a", maxControlFrame) + "\x07", invalid},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, new(IO).scanControlFrame([]byte(tc.in)))
		})
	}
}

// scriptedKeys reads keys from in.
func scriptedKeys(in io.Reader) *IO {
	return &IO{In: &mockDescriptor{Reader: in, fd: 0}, SplitKeys: true}
}

// nextEvent reads one event and waits for its reader to finish.
func nextEvent(t *testing.T, tio *IO) KeyEvent {
	t.Helper()
	ev := <-tio.ReadEvents(t.Context(), nil)
	tio.AwaitReader()
	return ev
}

// readKeys reads events until EOF and returns the keys that are not ignored.
func readKeys(t *testing.T, tio *IO) string {
	t.Helper()
	var keys strings.Builder
	for {
		ev := nextEvent(t, tio)
		if ev.Err != nil {
			assert.ErrorIs(t, ev.Err, io.EOF)
			return keys.String()
		}
		if ev.Key != KeyIgnored {
			keys.WriteRune(ev.Key)
		}
	}
}

func TestTermIO_ControlReplyIsNotAKey(t *testing.T) {
	for name, tc := range map[string]struct{ in, keys string }{
		"alone then key": {oscReply + "a", "a"},
		"key then reply": {"a" + oscReply, "a"},
		"coalesced":      {"a" + oscReply + "b", "ab"},
		"st terminator":  {"\x1b]11;rgb:0/0/0\x1b\\a", "a"},
		"arrow after":    {oscReply + "\x1b[A", "↑"},
		"two replies":    {oscReply + oscReply + "a", "a"},
	} {
		t.Run(name, func(t *testing.T) {
			tio := scriptedKeys(bytes.NewBufferString(tc.in))
			q := tio.expectControl(11)
			defer q.cancel()
			assert.Equal(t, tc.keys, readKeys(t, tio))
			assert.Equal(t, 1, len(q.reply))
			assert.Equal(t, 11, (<-q.reply).code)
		})
	}
}

func TestTermIO_ControlReplyKeepsKeyPermit(t *testing.T) {
	tio := scriptedKeys(bytes.NewBufferString(oscReply + "a"))
	permits := make(chan struct{}, 1)
	permits <- struct{}{} // a single key may be read
	ev := <-tio.ReadEvents(t.Context(), permits)
	assert.NoError(t, ev.Err)
	assert.Equal(t, 'a', ev.Key)
}

func TestTermIO_ControlReplySplitAcrossReads(t *testing.T) {
	tio := scriptedKeys(iotest.OneByteReader(strings.NewReader("\x1b]11;rgb:0000/0000/0000\x1b\\q")))
	q := tio.expectControl(11)
	assert.Equal(t, 'q', nextEvent(t, tio).Key)
	assert.Equal(t, "rgb:0000/0000/0000", (<-q.reply).data)
}

func TestTermIO_LateControlReplyIsDropped(t *testing.T) {
	tio := scriptedKeys(bytes.NewBufferString(oscReply + "a"))
	tio.expectControl(11).cancel() // the query already timed out
	assert.Equal(t, 'a', nextEvent(t, tio).Key)
}

func TestTermIO_ControlReplyWrongCodeStaysQueued(t *testing.T) {
	tio := scriptedKeys(bytes.NewBufferString(oscReply + "a"))
	q := tio.expectControl(10)
	defer q.cancel()
	assert.Equal(t, 'a', nextEvent(t, tio).Key)
	assert.Equal(t, 0, len(q.reply)) // a reply for another code is not delivered
}

func TestTermIO_AltBracketStillAKey(t *testing.T) {
	tio := &IO{}
	n, complete := tio.escapeSequenceLen([]byte("\x1b]"))
	assert.Equal(t, 2, n)
	assert.Equal(t, false, complete) // waits [escapeTimeout] for a header, then it is Alt+]
	key, consumed, err := tio.decodeRuneBytes([]byte("\x1b]"), 2)
	assert.NoError(t, err)
	assert.Equal(t, rune(KeyIgnored), key)
	assert.Equal(t, 2, consumed)
}

func TestTermIO_InvalidOSCIsNotSwallowed(t *testing.T) {
	// not a reply (no numeric code), so only Alt+] is dropped and the text stays typed
	tio := scriptedKeys(bytes.NewBufferString("\x1b]xy"))
	assert.Equal(t, "xy", readKeys(t, tio))
}

func TestTermIO_PasteWithControlReplyStaysPaste(t *testing.T) {
	in := "\x1b[200~" + oscReply + "\x1b[201~"
	tio := &IO{In: &mockDescriptor{Reader: bytes.NewBufferString(in), fd: 0}, BracketedPaste: true}
	q := tio.expectControl(11)
	defer q.cancel()
	assert.Equal(t, oscReply, string(nextEvent(t, tio).Paste))
	assert.Equal(t, 0, len(q.reply)) // the paste payload is not routed as a reply
}

func TestChanIO_WriteControlIsVerbatimAndNotReplayed(t *testing.T) {
	out := &syncBuffer{}
	cio := startChanIOTo(t.Context(), out, 80, 24)

	vp, err := cio.PushViewport()
	assert.NoError(t, err)
	_, err = vp.Write([]byte("prompt"))
	assert.NoError(t, err)
	assert.NoError(t, cio.Flush())

	const query = "\x1b]11;?\x07"
	assert.NoError(t, cio.writeControl(t.Context(), []byte(query)))
	assert.Equal(t, 1, strings.Count(out.String(), query))

	// redraws of the managed rows must not repeat the request
	_, err = vp.Write([]byte(" more"))
	assert.NoError(t, err)
	assert.NoError(t, cio.Flush())
	cio.releaseViewport(vp)
	assert.NoError(t, cio.Flush())
	assert.Equal(t, 1, strings.Count(out.String(), query))
}

func TestChanIO_WriteControlNotHeldBackByPartialLine(t *testing.T) {
	out := &syncBuffer{}
	cio := startChanIOTo(t.Context(), out, 80, 24)
	vp, err := cio.PushViewport()
	assert.NoError(t, err)
	defer cio.releaseViewport(vp)
	_, err = cio.Write([]byte("no newline yet"))
	assert.NoError(t, err)
	assert.NoError(t, cio.writeControl(t.Context(), []byte("\x1b]11;?\x07")))
	assert.Contains(t, out.String(), "\x1b]11;?\x07")
}

func TestChanIO_WriteControlCancelled(t *testing.T) {
	cio := NewUnstartedIO(t.Context(), 80, 24, 0) // nothing serves the channel
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, cio.writeControl(ctx, []byte("x")), context.Canceled)
}

func TestTermIO_WriteControl(t *testing.T) {
	out := &bytes.Buffer{}
	direct := &IO{Out: out, Fd: 2}
	assert.NoError(t, direct.writeControl(t.Context(), []byte("q")))
	assert.Equal(t, "q", out.String())

	scripted := &IO{Out: out}
	assert.ErrorIs(t, scripted.writeControl(t.Context(), []byte("q")), errs.ErrNoTTY)
	assert.Equal(t, "q", out.String())
}
