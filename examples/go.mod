module github.com/nfx/go-tui/examples

go 1.27

require (
	github.com/lmittmann/tint v1.2.0
	github.com/nfx/go-tui v0.0.0
	golang.org/x/term v0.46.0
)

require golang.org/x/sys v0.48.0 // indirect

replace github.com/nfx/go-tui => ..
