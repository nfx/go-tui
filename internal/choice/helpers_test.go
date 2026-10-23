// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"iter"
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
