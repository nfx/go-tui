// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

type tableEvent interface {
	isTableEvent()
}

type tableEventMarker struct{}

func (tableEventMarker) isTableEvent() {}

type tableBegin struct {
	tableEventMarker
	Columns []tableColumnInfo
}

type tableColumnInfo struct {
	Header string
	Kind   string
}

type tableRow struct {
	tableEventMarker
	Cells []string
}

type tableEnd struct {
	tableEventMarker
	Rows int
}
