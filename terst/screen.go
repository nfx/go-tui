// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package terst

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// screen applies terminal output to a grid of cells, so that tests see what
// a user would see.
type screen struct {
	width int
	// height, if positive, bounds the grid like a terminal, which scrolls
	// its top row off once the cursor moves below the bottom row
	height   int
	cells    [][]rune
	row, col int
	wrap     bool   // the last column is written, the next rune goes on the next row
	esc      []byte // an escape sequence that is not complete yet
}

func (s *screen) write(p []byte) {
	for i := 0; i < len(p); {
		b := p[i]
		switch {
		case len(s.esc) > 0:
			s.escape(b)
		case b == 0x1b:
			s.esc = []byte{b}
		case b == '\r':
			s.col, s.wrap = 0, false
		case b == '\n':
			s.down()
			s.wrap = false
		case b < 0x20:
		default:
			r, size := utf8.DecodeRune(p[i:])
			s.put(r)
			i += size
			continue
		}
		i++
	}
}

func (s *screen) line(row int) []rune {
	for len(s.cells) <= row {
		s.cells = append(s.cells, []rune(strings.Repeat(" ", s.width)))
	}
	if len(s.cells[row]) < s.width {
		s.cells[row] = append(s.cells[row], []rune(strings.Repeat(" ", s.width-len(s.cells[row])))...)
	}
	return s.cells[row]
}

func (s *screen) put(r rune) {
	if s.wrap {
		s.down()
		s.col, s.wrap = 0, false
	}
	s.line(s.row)[s.col] = r
	if s.col == s.width-1 {
		s.wrap = true
	} else {
		s.col++
	}
}

// down moves the cursor to the next row, scrolling a bounded screen at its
// bottom row.
func (s *screen) down() {
	s.row++
	if s.height > 0 && s.row >= s.height {
		s.line(s.row)
		drop := s.row - s.height + 1
		s.cells = s.cells[drop:]
		s.row -= drop
	}
}

func (s *screen) escape(b byte) {
	s.esc = append(s.esc, b)
	if s.esc[1] == ']' { // an OSC request, ended by BEL or ST, draws nothing
		if b == 0x07 || b == '\\' && s.esc[len(s.esc)-2] == 0x1b {
			s.esc = nil
		}
		return
	}
	if len(s.esc) == 2 && b != '[' {
		s.esc = nil // a two-byte escape, ignored
		return
	}
	if len(s.esc) < 3 || b < 0x40 || b > 0x7e {
		return
	}
	params := strings.TrimPrefix(string(s.esc[2:len(s.esc)-1]), "?")
	s.esc = nil
	n, err := strconv.Atoi(strings.Split(params, ";")[0])
	if err != nil {
		n = 0
	}
	s.wrap = false
	switch b {
	case 'A':
		s.row = max(0, s.row-max(1, n))
	case 'B':
		s.row += max(1, n)
		if s.height > 0 {
			s.row = min(s.row, s.height-1)
		}
	case 'C':
		s.col = min(s.width-1, s.col+max(1, n))
	case 'D':
		s.col = max(0, s.col-max(1, n))
	case 'K':
		line := s.line(s.row)
		from, to := s.col, len(line)
		switch n {
		case 1:
			from, to = 0, s.col+1
		case 2:
			from = 0
		}
		for i := from; i < to; i++ {
			line[i] = ' '
		}
	}
}

// rows returns the rows that are not blank, without surrounding spaces.
func (s *screen) rows() (rows []string) {
	for _, line := range s.cells {
		if row := strings.TrimSpace(string(line)); row != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

// shows reports whether the screen shows exactly the wanted rows, as
// [Output.AwaitVisible] matches them.
func (s *screen) shows(want []string) bool {
	rows := s.rows()
	if len(rows) != len(want) {
		return false
	}
	for i, w := range want {
		prefix, ok := strings.CutSuffix(w, "...")
		if ok && !strings.HasPrefix(rows[i], prefix) || !ok && rows[i] != w {
			return false
		}
	}
	return true
}
