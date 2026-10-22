// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package prompt

type inputOutgoing interface {
	isInputOutgoing()
}

type inputOutgoingMarker struct{}

func (inputOutgoingMarker) isInputOutgoing() {}

type inputInit struct {
	inputOutgoingMarker
	Label    string
	Password bool
}

type inputComplete struct {
	inputOutgoingMarker
	Value string
}

type inputIncoming interface {
	isInputIncoming()
}

type inputIncomingMarker struct{}

func (inputIncomingMarker) isInputIncoming() {}

type inputChanged struct {
	inputIncomingMarker
	Text string
	// Cursor is the rune index of the cursor after the change. When nil, the
	// cursor is derived from the change in text length.
	Cursor *int
}

type inputConfirmed struct {
	inputIncomingMarker
}
