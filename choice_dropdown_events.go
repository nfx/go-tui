// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

type dropdownOutputEvent interface {
	isDropdownOutputEvent()
}

type dropdownOutputEventMarker struct{}

func (dropdownOutputEventMarker) isDropdownOutputEvent() {}

type dropdownInit struct {
	dropdownOutputEventMarker
	Label string
}

type dropdownAppendItem struct {
	dropdownOutputEventMarker
	Item  any
	Index int
	Text  string
}

type dropdownFilterChanged struct {
	dropdownOutputEventMarker
	Prefix   string
	Matching int
	Added    []int
	Removed  []int
}

type dropdownConfirmed struct {
	dropdownOutputEventMarker
	Selected int
}

type dropdownState struct {
	prefix   string
	relevant []int
}

type dropdownInputEvent interface {
	isDropdownInputEvent()
}

type dropdownInputEventMarker struct{}

func (dropdownInputEventMarker) isDropdownInputEvent() {}

type dropdownInputConfirmed struct {
	dropdownInputEventMarker
	Index int
}

type dropdownFilteredWith struct {
	dropdownInputEventMarker
	Prefix string
}

type itPair struct {
	item any
	err  error
}

// Equal compares two dropdown state snapshots.
func (d dropdownState) Equal(other dropdownState) bool {
	if d.prefix != other.prefix {
		return false
	}
	if len(d.relevant) != len(other.relevant) {
		return false
	}
	for i := range d.relevant {
		if d.relevant[i] != other.relevant[i] {
			return false
		}
	}
	return true
}

// Diff returns added and removed relevant indexes between two sorted snapshots.
func (d dropdownState) Diff(prev dropdownState) (added []int, removed []int) {
	var i, j int
	for i < len(prev.relevant) && j < len(d.relevant) {
		a := prev.relevant[i]
		b := d.relevant[j]
		if a == b {
			i++
			j++
			continue
		}
		if a < b {
			removed = append(removed, a)
			i++
			continue
		}
		added = append(added, b)
		j++
	}
	for ; i < len(prev.relevant); i++ {
		removed = append(removed, prev.relevant[i])
	}
	for ; j < len(d.relevant); j++ {
		added = append(added, d.relevant[j])
	}
	return added, removed
}
