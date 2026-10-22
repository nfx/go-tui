// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/nfx/go-tui/internal/ansi"
	"github.com/nfx/go-tui/internal/color"
	"github.com/nfx/go-tui/theme"
	"io"
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
)

func TestFactsBasicRender(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Name string
		Age  int
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Name: "Alice", Age: 30})
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "AGE")
	assert.Contains(t, lines[1], "Alice")
	assert.Contains(t, lines[1], "30")
}

func TestFactsWrapsOnNarrowTTY(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 14, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Alpha: "a", Beta: "b", Gamma: "c", Delta: "d"})
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
	assert.Contains(t, buf.String(), "\n\n")
	assert.Contains(t, lines[0], "ALPHA")
	assert.Contains(t, lines[0], "BETA")
	assert.NotContains(t, lines[0], "GAMMA")
	assert.Contains(t, lines[1], "a")
	assert.Contains(t, lines[1], "b")
	assert.Contains(t, lines[2], "GAMMA")
	assert.Contains(t, lines[2], "DELTA")
	assert.Contains(t, lines[3], "c")
	assert.Contains(t, lines[3], "d")
}

func TestFactsWithMaxWidthCapsWideTTY(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Alpha: "a", Beta: "b", Gamma: "c", Delta: "d"}, WithMaxWidth(14))
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
	assert.Contains(t, lines[2], "GAMMA")
}

func TestFactsWithMaxWidthDoesNotWidenNarrowTTY(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 14, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Alpha: "a", Beta: "b", Gamma: "c", Delta: "d"}, WithMaxWidth(80))
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
}

func TestFactsHonorsColumnOptions(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	data := tableFormatRow{Name: "A", Rate: 0.1, Amount: 9}
	buf := &bytes.Buffer{}
	err := Facts(buf, data,
		WithIncludeColumns("Name", "Rate"),
		WithColumnFormat("Rate", func(v float64) string { return fmt.Sprintf("rate=%.1f", v) }),
	)
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "RATE")
	assert.NotContains(t, lines[0], "AMOUNT")
	assert.Contains(t, lines[1], "A")
	assert.Contains(t, lines[1], "rate=0.1")
}

func TestFactsAlignsColumnStartsAcrossGroups(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Alpha string
		Beta  string
		Gamma string
		Delta string
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{
		Alpha: "1234567",
		Beta:  "B",
		Gamma: "g",
		Delta: "d",
	}, WithMaxWidth(24))
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 4, len(lines))
	betaStart := strings.Index(lines[0], "BETA")
	deltaStart := strings.Index(lines[2], "DELTA")
	assert.True(t, betaStart >= 0)
	assert.Equal(t, betaStart, deltaStart)
}

func TestFactsAddsTrailingBlankLine(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Name string
		Age  int
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Name: "Alice", Age: 30})
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(buf.String(), "\n\n"))
}

func TestFactsHonorsAlignRightMetadata(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		Left  string
		Right string `header:"RIGHT,align-right"`
	}
	buf := &bytes.Buffer{}
	err := Facts(buf, row{Left: "left", Right: "x"})
	assert.NoError(t, err)

	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	rightHeaderStart := strings.Index(lines[0], "RIGHT")
	rightValueStart := strings.Index(lines[1], "x")
	assert.True(t, rightHeaderStart >= 0)
	assert.True(t, rightValueStart > rightHeaderStart)
}

func TestFactsInterfaceInputUsesDynamicType(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	var data any = struct{ Name string }{"Alice"}
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, data))
	lines := factLines(buf.String())
	assert.Equal(t, 2, len(lines))
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[1], "Alice")

	var nilData any
	assert.Error(t, Facts(&bytes.Buffer{}, nilData))
	assert.Error(t, Facts(&bytes.Buffer{}, any(42)))
}

func TestFactsTabIsMeasured(t *testing.T) {
	orig := termGetSize
	termGetSize = func(int) (int, int, error) { return 120, 40, nil }
	t.Cleanup(func() { termGetSize = orig })

	type row struct {
		A string
		B string
	}
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, row{A: "a\tb", B: "x"}))
	assert.NotContains(t, buf.String(), "\t")
	lines := factLines(buf.String())
	assert.Equal(t, strings.Index(lines[0], "B"), strings.Index(lines[1], "x"))
}

func TestThemedFactsMatchesLegacyOnPlainWriter(t *testing.T) {
	type four struct{ Alpha, Beta, Gamma, Delta string }
	render := func() []string {
		var outs []string
		for _, tc := range []struct {
			width int
			run   func(w io.Writer) error
		}{
			{120, func(w io.Writer) error { return Facts(w, factsData) }},
			{14, func(w io.Writer) error { return Facts(w, four{"a", "b", "c", "d"}) }},
			{120, func(w io.Writer) error { return Facts(w, four{"1234567", "B", "g", "d"}, WithMaxWidth(24)) }},
			{120, func(w io.Writer) error { return Facts(w, factsData, WithMaxWidth(12)) }},
			{120, func(w io.Writer) error {
				return Facts(w, tableFormatRow{"A", 0.1, 9}, WithIncludeColumns("Name", "Rate"),
					WithColumnFormat("Rate", func(v float64) string { return fmt.Sprintf("rate=%.1f", v) }))
			}},
			{120, func(w io.Writer) error { return Facts[any](w, dummyPets[0]) }},
		} {
			ttyWidth(t, tc.width)
			buf := &bytes.Buffer{}
			assert.NoError(t, tc.run(buf))
			outs = append(outs, buf.String())
		}
		return outs
	}
	withEnv(t)
	legacy := render()
	themedTable(t, theme.New())
	themed := render()
	assert.Equal(t, legacy, themed)
	for _, out := range themed {
		assert.NotContains(t, out, "\x1b")
	}
}

func TestThemedFactsRoles(t *testing.T) {
	ttyWidth(t, 120)
	th := theme.New()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, factsData))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).Roles
	assert.True(t, len(roles.Foreground) > 0)
	out := buf.String()
	assert.Contains(t, out, ansi.Bold+string(roles.Foreground)+"OWNER.NAME"+color.FgDefault+ansi.Reset)
	assert.Contains(t, out, string(roles.Foreground)+"Alice"+color.FgDefault)
	for _, role := range []ansi.Text{roles.Accent, roles.Success, roles.Failure, roles.Muted} {
		assert.NotContains(t, out, string(role))
	}
	plain := &bytes.Buffer{}
	withEnv(t)
	assert.NoError(t, Facts(plain, factsData))
	assert.Equal(t, plain.String(), stripSGR(out))
}

func TestThemedFactsSlots(t *testing.T) {
	ttyWidth(t, 120)
	th := theme.New()
	th.Templates.Table.Header = `{{ .Name }}:{{ .Kind }}`
	th.Templates.Table.Cell = `[{{ .Value }}]`
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, factsRow{Left: "l", Count: 3, Owner: Person{Name: "Bo"}},
		WithColumnTemplate("Left", "T:{{.Left}}"),
		WithColumnFormat("Count", func(int) string { return "named" }),
		WithColumnTypeFormat(func(int) string { return "typed" })))
	// the value wraps the chosen format; an empty value stays empty
	assert.Equal(t, "Left:string   Count:int  Right:string Owner.Name:string  Owner.Age:int  \n"+
		"[T:l]           [named]               [Bo]               [typed]        \n\n", buf.String())
}

func TestThemedFactsHiddenKey(t *testing.T) {
	ttyWidth(t, 120)
	th := theme.New()
	th.Templates.Table.Header = theme.Hidden
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, factsData))
	assert.Equal(t, "left   7  x Alice  30  \n\n", buf.String())
}

func TestThemedFactsStyle(t *testing.T) {
	ttyWidth(t, 120)
	th := theme.New()
	th.Table = theme.TableStyle{ColumnGap: 4, MinColumnWidth: 3}
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, tableFormatRow{"A", 0.5, 1}))
	assert.Equal(t, "NAME       RATE    AMOUNT \nA          0.50         1 \n\n", buf.String())

	// the minimum column width is a floor under the max width, as in tables
	buf.Reset()
	assert.NoError(t, Facts(buf, tableFormatRow{"Alpha", 0.5, 1}, WithMaxWidth(4)))
	assert.Equal(t, "NA…    \nAl…    \n\n   RA… \n   0.… \n\n   AM… \n     1 \n\n", buf.String())
}

func TestThemedFactsSignedPercent(t *testing.T) {
	ttyWidth(t, 120)
	themedTable(t, theme.New(), "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Facts(buf, tableFormatRow{"A", -0.5, 1}, WithFloat64AsPercentPositiveColored()))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).Roles
	assert.Contains(t, buf.String(), string(roles.Failure)+"-50%"+color.FgDefault)
	assert.NotContains(t, buf.String(), "\x1b[31m")
}

func TestThemedFactsValueLine(t *testing.T) {
	ttyWidth(t, 120)
	type row struct {
		Note string
		Link string
	}
	th := theme.New()
	th.Templates.Table.Cell = `<{{ .Value }}>`
	themedTable(t, th)
	link := "\x1b]8;;https://example.com\x1b\\site\x1b]8;;\x1b\\"
	buf := &bytes.Buffer{}
	// the line the legacy presentation shows, without controls
	assert.NoError(t, Facts(buf, row{Note: "a\x07b\x1b[2Jc\nhidden", Link: link}))
	assert.Equal(t, "NOTE   LINK    \n<abc>  <"+link+">  \n\n", buf.String())
}

func TestThemedFactsErrors(t *testing.T) {
	ttyWidth(t, 120)
	boom := template.FuncMap{
		"boom": func(string) (string, error) { return "", errors.New("boom") },
		"bell": func() string { return "\a" },
	}
	for _, tc := range []struct {
		edit func(*theme.TableTemplates)
		want string
	}{
		{
			func(s *theme.TableTemplates) {
				s.Header = `{{ if eq .Name "Count" }}{{ boom .Header }}{{ end }}{{ .Header }}`
			},
			`facts: column "Count": theme.Templates.Table.Header: `,
		},
		{
			func(s *theme.TableTemplates) { s.Header = `{{ .Header }}{{ bell }}` },
			`facts: column "Left": theme.Templates.Table.Header: "\a": want text and SGR only`,
		},
		{func(s *theme.TableTemplates) { s.Cell = `{{ boom .Value }}` }, `facts: column "Left": theme.Templates.Table.Cell: `},
		{
			func(s *theme.TableTemplates) { s.Cell = `{{ .Value }}{{ bell }}` },
			`facts: column "Left": theme.Templates.Table.Cell: "\a": want text, SGR and hyperlinks only`,
		},
	} {
		th := theme.New()
		th.Funcs = boom
		tc.edit(&th.Templates.Table)
		themedTable(t, th)
		buf := &bytes.Buffer{}
		err := Facts(buf, factsData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
		assert.Equal(t, "", buf.String())
	}

	themedTable(t, theme.New())
	assert.ErrorIs(t, Facts(errWriterTable{}, factsData), io.EOF)
}
