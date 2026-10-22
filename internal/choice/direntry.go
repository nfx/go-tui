// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package choice

import (
	"errors"
	"os"
)

var upEntry = &dirEntry{name: "..", isDir: true}

type dirEntry struct {
	name  string
	isDir bool
}

func (d *dirEntry) String() string {
	if d.name == ".." {
		return ".."
	}
	if d.isDir {
		return "d " + d.name + string(os.PathSeparator)
	}
	return "- " + d.name + string(os.PathSeparator)
}

func (d *dirEntry) Name() string {
	return d.name
}

func (d *dirEntry) IsDir() bool {
	return d.isDir
}

func (d *dirEntry) Type() os.FileMode {
	if d.isDir {
		return os.ModeDir
	}
	return 0
}

func (d *dirEntry) Info() (os.FileInfo, error) {
	return nil, errors.New("not implemented")
}
