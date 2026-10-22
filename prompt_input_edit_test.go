// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"testing"
	"unicode/utf8"

	"github.com/nfx/go-tui/internal/assert"
	"github.com/nfx/go-tui/internal/fuzzseeds"
)

func (p *input) pressKey(tio *termIO) (string, error) {
	ev, ok := <-tio.readEvents(context.Background(), nil)
	tio.awaitReader()
	done, err := p.handleKeyEvent(p.ctx, ev, ok)
	if err != nil {
		return "", err
	}
	if done {
		return p.typed, nil
	}
	return "", nil
}

func TestInputPressKeyBackspace(t *testing.T) {
	i := &input{
		typed:  "ab",
		cursor: 2,
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{0x7f})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "a", i.typed)
	assert.Equal(t, 1, i.cursor)
}

func TestInputPressKeyArrowLeft(t *testing.T) {
	i := &input{
		typed:  "ab",
		cursor: 2,
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{0x1b, 0x5b, 0x44})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, 1, i.cursor)
}

func TestInputPressKeyEOF(t *testing.T) {
	i := &input{typed: ""}
	tio := &termIO{in: bytes.NewBuffer(nil)}
	_, err := i.pressKey(tio)
	assert.Error(t, err)
}

func TestInputPressKeyEnter(t *testing.T) {
	i := &input{typed: "done"}
	tio := &termIO{in: bytes.NewBuffer([]byte{keyEnter})}
	out, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "done", out)
}

func TestInputPressKeyAddsChar(t *testing.T) {
	i := &input{typed: "a", cursor: 1}
	tio := &termIO{in: bytes.NewBuffer([]byte{'b'})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "ab", i.typed)
	assert.Equal(t, 2, i.cursor)
}

func TestInputPressKeyPaste(t *testing.T) {
	i := &input{}
	tio := &termIO{in: bytes.NewBufferString("ab")}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	runes := []rune(i.typed)
	if len(runes) < 2 || runes[0] != 'a' || runes[1] != 'b' {
		t.Fatalf("unexpected paste result %q", i.typed)
	}
}

func TestInputPressKeyDoesNotEmitLowLevelOutputEvents(t *testing.T) {
	i := &input{
		Label:  "Label",
		typed:  "a",
		cursor: 1,
	}
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{'b'})}
	_, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(events))
}

func TestInputPressKeyEnterEmitsCompletedEvent(t *testing.T) {
	i := &input{
		Label:  "Label",
		typed:  "done",
		cursor: 4,
	}
	var events []inputOutgoing
	i.eventSink = func(ev inputOutgoing) {
		events = append(events, ev)
	}
	tio := &termIO{in: bytes.NewBuffer([]byte{keyEnter})}
	out, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "done", out)
	assert.Equal(t, 1, len(events))
	completed, ok := events[0].(inputComplete)
	assert.True(t, ok)
	assert.Equal(t, "done", completed.Value)
}

func TestInputPressKeyEnterWithNonEmptyRejectsEmpty(t *testing.T) {
	i := newInput(t.Context(), "Label")
	assert.NoError(t, WithNonEmpty()(i))
	tio := &termIO{in: bytes.NewBuffer([]byte{keyEnter})}
	out, err := i.pressKey(tio)
	assert.NoError(t, err)
	assert.Equal(t, "", out)
	assert.Equal(t, "", i.typed)
}

func TestInputCursorActions(t *testing.T) {
	i := &input{
		typed:  "go",
		cursor: 2,
	}
	pressBackspace(i)
	assert.Equal(t, "g", i.typed)
	assert.Equal(t, 1, i.cursor)

	i.pressLeft()
	assert.Equal(t, 0, i.cursor)
	i.pressRight()
	assert.Equal(t, 1, i.cursor)

	i.pressAny('a')
	assert.Equal(t, "ga", i.typed)
}

func TestInputCursorActionsMultiByte(t *testing.T) {
	i := &input{
		typed:  "\u65e5\u672c\u8a9e",
		cursor: 3,
	}
	i.pressLeft()
	pressBackspace(i)
	assert.Equal(t, "\u65e5\u8a9e", i.typed)
	assert.Equal(t, 1, i.cursor)

	i.pressAny('é')
	assert.Equal(t, "\u65e5é\u8a9e", i.typed)
	assert.Equal(t, 2, i.cursor)
	i.pressRight()
	i.pressRight()
	assert.Equal(t, 3, i.cursor)
}

func TestInputPasteMultiByte(t *testing.T) {
	i := &input{typed: "a", cursor: 1}
	paste := []byte("\u65e5\u672cé")
	done, err := i.handleKeyEvent(t.Context(), keyEvent{
		err:   &pasteTextError{buf: paste},
		paste: paste,
	}, true)
	assert.NoError(t, err)
	assert.True(t, !done)
	assert.Equal(t, "a\u65e5\u672cé", i.typed)
	assert.Equal(t, 4, i.cursor)
}

// regression: a backspace that deletes nothing must not move the cursor
func TestInputBackspaceAtStartKeepsCursor(t *testing.T) {
	i := &input{typed: "abc", cursor: 0}
	done, err := i.handleKeyEvent(t.Context(), keyEvent{key: 0x7f}, true)
	assert.NoError(t, err)
	assert.True(t, !done)
	assert.Equal(t, "abc", i.typed)
	assert.Equal(t, 0, i.cursor)
}

func TestInputCursorMovesByCluster(t *testing.T) {
	i := &input{typed: "aé👨‍👩b", cursor: 0}
	i.pressRight()
	assert.Equal(t, 1, i.cursor)
	i.pressRight() // over e and its accent
	assert.Equal(t, 3, i.cursor)
	i.pressRight() // over the whole ZWJ sequence
	assert.Equal(t, 6, i.cursor)
	i.pressLeft()
	assert.Equal(t, 3, i.cursor)
	pressBackspace(i) // deletes e with its accent
	assert.Equal(t, "a👨‍👩b", i.typed)
	assert.Equal(t, 1, i.cursor)
}

// pressBackspace applies a backspace the way a key event does.
func pressBackspace(i *input) {
	i.applyInputEvent(i.decodeInputEvent(0x7f))
}

func TestInputBackspaceByCluster(t *testing.T) {
	for _, tt := range []struct {
		name, typed string
		cursor      int
		password    bool
		want        string
		wantCursor  int
	}{
		{"combining mark", "ae\u0301b", 3, false, "ab", 1},
		{"flag", "a🇺🇦", 3, false, "a", 1},
		{"skin tone", "👍🏽x", 2, false, "x", 0},
		{"ZWJ sequence", "a👨\u200d👩\u200d👧", 6, false, "a", 1},
		{"keycap", "1\ufe0f\u20e3", 3, false, "", 0},
		{"at the start", "e\u0301", 0, false, "e\u0301", 0},
		{"beyond the end", "ae\u0301", 9, false, "a", 1},
		{"masked by rune", "ae\u0301", 3, true, "ae", 2},
		{"masked flag by rune", "🇺🇦", 2, true, "🇺", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := &input{typed: tt.typed, cursor: tt.cursor, Password: tt.password}
			pressBackspace(i)
			assert.Equal(t, tt.want, i.typed)
			assert.Equal(t, tt.wantCursor, i.cursor)
		})
	}
}

func FuzzInputWindow(f *testing.F) {
	for _, seed := range fuzzseeds.Text {
		f.Add(seed, uint8(3), uint8(5))
		f.Add(seed, uint8(0), uint8(2))
	}
	f.Fuzz(func(t *testing.T, s string, cursor, avail uint8) {
		i := &input{typed: s, cursor: int(cursor)}
		runes := i.displayRunes()
		cells := Text(string(runes)).Clusters()
		first, last := i.visibleWindow(cells, int(avail))
		if first < 0 || first > last || last > len(cells) {
			t.Fatalf("window [%d,%d) of %d clusters", first, last, len(cells))
		}
		visible := string(runes[cells.RuneOffset(first):cells.RuneOffset(last)])
		if avail > 0 && Text(visible).Width() > int(avail) {
			t.Fatalf("window %q wider than %d", visible, avail)
		}
		for range 3 {
			i.pressLeft()
			i.pressRight()
		}
		pressBackspace(i)
		if !utf8.ValidString(i.typed) && utf8.ValidString(s) {
			t.Fatalf("backspace broke UTF-8: %q", i.typed)
		}
	})
}
