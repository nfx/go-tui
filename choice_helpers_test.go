// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"iter"
	"strconv"
	"strings"
	"unicode/utf8"
)

type server struct {
	Name string
	Port int
}

type tagged struct {
	Name string
	ID   int
}

// seq yields values and records whether it ran.
func seq[T any](started *bool, err error, values ...T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		*started = true
		for _, v := range values {
			if !yield(v, nil) {
				return
			}
		}
		if err != nil {
			var zero T
			yield(zero, err)
		}
	}
}

// boundedScreen is a terminal of a fixed size that scrolls: a newline on the
// bottom row moves the top row into scrolled. It knows what newline-terminated
// frames and their cleanup write: CR, LF, cursor up and down, and erase line.
type boundedScreen struct {
	cells    [][]rune
	scrolled []string
	row, col int
}

func newBoundedScreen(width, height int) *boundedScreen {
	s := &boundedScreen{}
	for range height {
		s.cells = append(s.cells, []rune(strings.Repeat(" ", width)))
	}
	return s
}

// write applies out to the screen.
func (s *boundedScreen) write(out string) {
	for i := 0; i < len(out); {
		r, size := utf8.DecodeRuneInString(out[i:])
		i += size
		switch {
		case r == '\r':
			s.col = 0
		case r == '\n' && s.row == len(s.cells)-1:
			s.scrolled = append(s.scrolled, strings.TrimSpace(string(s.cells[0])))
			s.cells = append(s.cells[1:], []rune(strings.Repeat(" ", len(s.cells[0]))))
		case r == '\n':
			s.row++
		case r == 0x1b && strings.HasPrefix(out[i:], "["):
			end := strings.IndexFunc(out[i+1:], func(r rune) bool { return r >= 0x40 && r <= 0x7e }) + i + 1
			n, err := strconv.Atoi(out[i+1 : end])
			if err != nil {
				n = 1
			}
			switch out[end] {
			case 'A':
				s.row = max(0, s.row-n)
			case 'B':
				s.row = min(len(s.cells)-1, s.row+n)
			case 'K':
				for c := s.col; c < len(s.cells[s.row]); c++ {
					s.cells[s.row][c] = ' '
				}
			}
			i = end + 1
		case s.col < len(s.cells[s.row]):
			s.cells[s.row][s.col] = r
			s.col++
		}
	}
}

// visible returns the rows that are not blank, without surrounding spaces.
func (s *boundedScreen) visible() (rows []string) {
	for _, line := range s.cells {
		if row := strings.TrimSpace(string(line)); row != "" {
			rows = append(rows, row)
		}
	}
	return rows
}
