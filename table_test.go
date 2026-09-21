// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"iter"
	"testing"

	"github.com/nfx/go-tui/internal/assert"
)

type Pet struct {
	Name  string
	Age   int
	Type  string
	Owner Person
}

type Person struct {
	Name string
	Age  int
}

var dummyPets = []Pet{
	{"Fluffy", 3, "Cat", Person{"Alice", 30}},
	{"Buddy", 5, "Dog", Person{"Bob", 25}},
	{"Goldie", 1, "Fish", Person{"Charlie", 20}},
	{"Tweety", 2, "Bird", Person{"Diana", 35}},
	{"Nemo", 1, "Fish", Person{"Eve", 28}},
	{"Max", 4, "Dog", Person{"Frank", 40}},
	{"Whiskers", 2, "Cat", Person{"Grace", 22}},
}

func iterate[T any](items []T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, v := range items {
			if !yield(v, nil) {
				return
			}
		}
	}
}

func TestTableRender(t *testing.T) {
	buf := &bytes.Buffer{}
	err := Table(buf, "{{ bold .Name | green }}\t{{ .Age }}\t{{ .Type }}\t{{ .Owner.Name }}\t{{ .Owner.Age }}", iterate(dummyPets))
	assert.NoError(t, err)
}
