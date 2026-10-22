// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"text/template"

	"github.com/nfx/go-tui/internal/assert"
)

// themedTable turns the theme gate on with th installed and clears the color
// environment, then adds pairs such as FORCE_COLOR=3.
func themedTable(t *testing.T, th Theme, env ...string) {
	t.Helper()
	withEnv(t, env...)
	useTheme(t, th)
}

func TestThemedTableMatchesLegacyOnPlainWriter(t *testing.T) {
	render := func() []string {
		var outs []string
		for _, run := range []func(w io.Writer) error{
			func(w io.Writer) error { return TableAuto(w, dummyPets) },
			func(w io.Writer) error { return TableAuto(w, dummyPets, WithMaxWidth(30)) },
			func(w io.Writer) error { return Table(w, "{{.Name}}\t{{.Owner.Name}}\t{{.Age}}", dummyPets) },
			func(w io.Writer) error {
				return Table(w, "", []themedRow{{"a\nb", 1, "c\nd\ne"}}, WithMultilineCells())
			},
		} {
			buf := &bytes.Buffer{}
			assert.NoError(t, run(buf))
			outs = append(outs, buf.String())
		}
		return outs
	}
	withEnv(t)
	legacy := render()
	themedTable(t, NewTheme())
	themed := render()
	assert.Equal(t, legacy, themed)
	for _, out := range themed {
		assert.NotContains(t, out, "\x1b")
	}
}

func TestThemedTableRoles(t *testing.T) {
	th := NewTheme()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	assert.True(t, len(roles.foreground) > 0)
	out := buf.String()
	assert.Contains(t, out, Bold+string(roles.foreground)+"NAME"+FgDefault+Reset)
	assert.Contains(t, out, string(roles.foreground)+"alpha"+FgDefault)
	assert.Contains(t, out, string(roles.foreground)+"333"+FgDefault)
	// ordinary data is neither an outcome nor a focus
	for _, role := range []Text{roles.accent, roles.success, roles.failure, roles.muted} {
		assert.NotContains(t, out, string(role))
	}
	assert.Equal(t, "NAME    AMOUNT NOTE   \nalpha        1 first  \n"+
		"beta        22        \ngamma      333 third  \n", stripSGR(out))
}

func TestThemedTableSlots(t *testing.T) {
	th := NewTheme()
	th.Templates.Table.Header = `{{ .Name }}:{{ .Kind }}`
	th.Templates.Table.Cell = `<{{ .Value }}>`
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows[:2]))
	assert.Equal(t, "Name:string   Amount:int Note:string  \n"+
		"<alpha>              <1> <first>      \n"+
		"<beta>              <22>              \n", buf.String())

	// an explicit row template keeps its cells; the header is still themed
	buf.Reset()
	assert.NoError(t, Table(buf, "{{.Name}}\t{{.Owner.Name}}\t{{.Owner.Age}}", dummyPets[1:2]))
	assert.Equal(t, "Name:string  Owner.Name:string  Owner.Age:int  \n"+
		"Fluffy       Alice              30             \n", buf.String())
}

func TestThemedTableHiddenHeader(t *testing.T) {
	th := NewTheme()
	th.Templates.Table.Header = Hidden
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows[:2]))
	assert.Equal(t, "alpha    1 first  \nbeta    22        \n", buf.String())
	buf.Reset()
	assert.NoError(t, Table(buf, "{{.Name}}", themedRows[:1]))
	assert.Equal(t, "alpha  \n", buf.String())
}

func TestThemedTableCellWrapsChosenFormat(t *testing.T) {
	th := NewTheme()
	th.Templates.Table.Cell = `[{{ .Value }}]`
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows[:1],
		WithColumnTemplate("Name", "T:{{.Name}}"),
		WithColumnFormat("Name", func(string) string { return "named" }),
		WithColumnFormat("Amount", func(int) string { return "named" }),
		WithColumnTypeFormat(func(int) string { return "typed" }),
		WithColumnTypeFormat(func(string) string { return "typed" }),
	))
	assert.Equal(t, "NAME         AMOUNT NOTE     \n[T:alpha]   [named] [typed]  \n", buf.String())
}

func TestThemedTableSignedPercent(t *testing.T) {
	data := []tableFormatRow{{"A", 0.05873242, 0}, {"B", -0.999999, 0}, {"C", 0, 0}}
	themedTable(t, NewTheme(), "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, "", data, WithFloat64AsPercentPositiveColored()))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	lines := strings.Split(buf.String(), "\n")
	assert.Contains(t, lines[1], string(roles.success)+"5.87%"+FgDefault)
	assert.Contains(t, lines[2], string(roles.failure)+"-99%"+FgDefault)
	assert.NotContains(t, lines[3], string(roles.success))
	assert.NotContains(t, lines[3], string(roles.failure))
	// the fixed ANSI colors are gone
	assert.NotContains(t, buf.String(), "\x1b[32m")
	assert.NotContains(t, buf.String(), "\x1b[31m")

	withEnv(t)
	buf.Reset()
	assert.NoError(t, Table(buf, "", data, WithFloat64AsPercentPositiveColored()))
	assert.Equal(t, "NAME    RATE  AMOUNT \nA      5.87%       0 \nB       -99%       0 \nC         0%       0 \n",
		buf.String())
}

func TestThemedTableScaleBuckets(t *testing.T) {
	type row struct {
		Value float64
	}
	data := []row{{0}, {100}, {25}, {50}, {75}, {0}}
	// rank buckets, as in TestWithColumnGreenRedScaleNumeric: the first
	// value has nothing to rank against
	want := []int{-1, 4, 2, 3, 3, 0}
	for _, force := range []string{"3", "2", "1"} {
		th := NewTheme()
		th.Light = true
		themedTable(t, th, "FORCE_COLOR="+force)
		for _, reversed := range []bool{false, true} {
			o := WithColumnGreenRedScale("Value")
			if reversed {
				o = WithColumnRedGreenScale("Value")
			}
			buf := &bytes.Buffer{}
			assert.NoError(t, TableAuto(buf, data, o))
			look := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil)
			scale := look.diverging()
			lines := strings.Split(buf.String(), "\n")
			for i, bucket := range want {
				line, value := lines[i+1], strings.TrimSpace(stripSGR(lines[i+1]))
				if bucket < 0 {
					// only the Cell slot's foreground
					assert.Equal(t, "   "+string(look.roles.foreground)+value+FgDefault+Reset+" ", line)
					continue
				}
				if reversed {
					bucket = len(scale) - 1 - bucket
				}
				// buckets follow ranks even where levels encode alike
				assert.Contains(t, line, string(scale[bucket])+value)
			}
		}
	}
}

func TestThemedTableExtraCellsAreDropped(t *testing.T) {
	render := func() string {
		buf := &bytes.Buffer{}
		assert.NoError(t, TableAuto(buf, themedRows[:1], WithColumnTemplate("Note", "{{.Note}}\textra")))
		return buf.String()
	}
	withEnv(t)
	legacy := render()
	themedTable(t, NewTheme())
	assert.Equal(t, legacy, render())
}

func TestThemedTableScaleWithoutColor(t *testing.T) {
	type row struct {
		Value int
	}
	themedTable(t, NewTheme())
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, []row{{1}, {2}, {3}}, WithColumnGreenRedScale("Value")))
	assert.Equal(t, " VALUE \n     1 \n     2 \n     3 \n", buf.String())
}

func TestThemedTableMultilineCarriesStyles(t *testing.T) {
	type row struct {
		Note string
		Name string
	}
	themedTable(t, NewTheme(), "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, "{{ underline .Note }}\t{{ .Name }}", []row{{"one\ntwo\n\nfour", "x"}},
		WithMultilineCells()))
	// every line closes its style before the next column; continuation
	// lines reopen it, except the empty one
	assert.Equal(t, Bold+"NOTE"+Reset+"  "+Bold+"NAME"+Reset+"  \n"+
		Underline+"one"+Reset+"   x     \n"+
		Underline+"two"+Reset+"         \n"+
		"            \n"+
		Underline+"four"+Reset+"        \n", buf.String())
}

func TestThemedTableStyle(t *testing.T) {
	th := NewTheme()
	th.Table = TableStyle{ColumnGap: 4, MinColumnWidth: 3}
	themedTable(t, th)
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, themedRows))
	assert.Equal(t, "NAME        AMOUNT NOTE     \n"+
		"alpha            1 first    \n"+
		"beta            22          \n"+
		"gamma          333 third    \n", buf.String())

	// the minimum column width is a floor under the max width
	buf.Reset()
	assert.NoError(t, TableAuto(buf, themedRows[:1], WithMaxWidth(10)))
	assert.Equal(t, "NA…       AM… NO…    \nal…         1 fi…    \n", buf.String())
}

func TestThemedTableWidthLockAfterFirstBatch(t *testing.T) {
	data := make([]themedRow, 12)
	for i := range data {
		data[i] = themedRow{Name: fmt.Sprintf("n%02d", i), Amount: i}
	}
	data[11] = themedRow{Name: "a much longer name", Amount: 123456789, Note: "much later"}
	th := NewTheme()
	th.Light = true
	themedTable(t, th)
	plain := &bytes.Buffer{}
	assert.NoError(t, TableAuto(plain, data))
	lines := strings.Split(plain.String(), "\n")
	assert.Equal(t, "a m…   12345… muc…  ", lines[12])

	// decoration does not count toward widths
	withEnv(t, "FORCE_COLOR=3")
	colored := &bytes.Buffer{}
	assert.NoError(t, TableAuto(colored, data))
	assert.True(t, colored.String() != plain.String())
	assert.Equal(t, plain.String(), stripSGR(colored.String()))
}

func TestThemedTableCellKeepsHyperlinksDropsControls(t *testing.T) {
	type row struct {
		Link string
	}
	themedTable(t, NewTheme())
	link := "\x1b]8;;https://example.com\x1b\\site\x1b]8;;\x1b\\"
	buf := &bytes.Buffer{}
	assert.NoError(t, TableAuto(buf, []row{{"a\x07b\x1b[2Jc"}, {link}}))
	assert.Equal(t, "LINK  \nabc   \n"+link+"  \n", buf.String())
}

func TestThemedTableRowTemplateHelpersBindToWriter(t *testing.T) {
	tmpl := "{{ underline .Name }}\t{{ success .Name }}\t{{ fg \"#FF0000\" .Name }}"
	themedTable(t, NewTheme())
	buf := &bytes.Buffer{}
	assert.NoError(t, Table(buf, tmpl, themedRows[:1]))
	assert.NotContains(t, buf.String(), "\x1b")

	withEnv(t, "FORCE_COLOR=3")
	buf.Reset()
	assert.NoError(t, Table(buf, tmpl, themedRows[:1]))
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil).roles
	assert.Contains(t, buf.String(), Underline+"alpha")
	assert.Contains(t, buf.String(), string(roles.success)+"alpha")
	assert.Contains(t, buf.String(), "\x1b[38;2;255;0;0malpha")
}

func TestThemedTableErrors(t *testing.T) {
	boom := template.FuncMap{
		"boom": func(string) (string, error) { return "", errors.New("boom") },
		"bell": func() string { return "\a" },
	}
	for _, tc := range []struct {
		edit func(*TableTemplates)
		want string
	}{
		{func(s *TableTemplates) { s.Header = `{{ boom .Header }}` }, "table: headers: theme.Templates.Table.Header: "},
		{
			func(s *TableTemplates) { s.Cell = `{{ if eq .Name "Amount" }}{{ boom .Value }}{{ end }}{{ .Value }}` },
			`row 0: append: render: column "Amount": theme.Templates.Table.Cell: `,
		},
		{
			func(s *TableTemplates) { s.Cell = `{{ .Value }}{{ bell }}` },
			`row 0: append: render: column "Name": theme.Templates.Table.Cell: "\a": want text, SGR and hyperlinks only`,
		},
	} {
		th := NewTheme()
		th.Funcs = boom
		tc.edit(&th.Templates.Table)
		themedTable(t, th)
		buf := &bytes.Buffer{}
		err := TableAuto(buf, themedRows)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
		assert.Equal(t, "", buf.String())
	}

	themedTable(t, NewTheme())
	err := TableAuto(errWriterTable{}, themedRows)
	assert.ErrorIs(t, err, io.EOF)
}

func TestThemedTableIterMatchesTable(t *testing.T) {
	data := streamedRows()
	for _, env := range [][]string{nil, {"FORCE_COLOR=3"}} {
		th := NewTheme()
		th.Light = true
		themedTable(t, th, env...)
		iterated, whole := &bytes.Buffer{}, &bytes.Buffer{}
		assert.NoError(t, TableIter(iterated, "", iterate(data)))
		assert.NoError(t, TableAuto(whole, data))
		assert.Equal(t, whole.String(), iterated.String())
		// widths lock after the first batch, decoration not counted
		assert.Equal(t, "a m…   12345… muc…  ", stripSGR(lines(iterated.String())[12]))
	}
	roles := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), &bytes.Buffer{}, nil).roles
	buf := &bytes.Buffer{}
	assert.NoError(t, TableIter(buf, "", iterate(data[:1])))
	fg := func(s string) string { return string(roles.foreground) + s + FgDefault }
	assert.Equal(t, Bold+fg("NAME")+Reset+"   "+Bold+fg("AMOUNT")+Reset+" "+Bold+fg("NOTE")+Reset+"  \n"+
		fg("n00")+Reset+"         "+fg("0")+Reset+"       \n", buf.String())
}

func TestThemedTableIterKeepsOneSnapshot(t *testing.T) {
	data := streamedRows()
	th := NewTheme()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	want := &bytes.Buffer{}
	assert.NoError(t, TableIter(want, "", iterate(data)))

	other := NewTheme()
	other.Templates.Table.Header = Hidden
	other.Templates.Table.Cell = `<{{ .Value }}>`
	other.Table = TableStyle{ColumnGap: 5}
	buf := &bytes.Buffer{}
	var firstBatch string
	changing := func(yield func(themedRow, error) bool) {
		for i, v := range data {
			switch i {
			case 0:
				// the theme is captured before the first row
				assert.NoError(t, defaultIO.installTheme(other))
			case 10:
				// the first batch is out, and later batches keep the
				// writer's capabilities
				firstBatch = buf.String()
				withEnv(t)
			}
			if !yield(v, nil) {
				return
			}
		}
	}
	assert.NoError(t, TableIter(buf, "", changing))
	assert.Equal(t, want.String(), buf.String())
	assert.Equal(t, strings.Join(lines(want.String())[:11], "\n")+"\n", firstBatch)

	// the next table takes the new theme and environment
	buf.Reset()
	assert.NoError(t, TableIter(buf, "", iterate(data[:1])))
	assert.Equal(t, "<n00>         <0>       \n", buf.String())
}

func TestThemedTableIterScaleRanksSeenSoFar(t *testing.T) {
	type row struct {
		Value int
	}
	var data []row
	for i := 1; i <= 10; i++ {
		data = append(data, row{i * 10})
	}
	data = append(data, row{0}, row{55}, row{100})
	// every value of the first batch is the largest so far; later ones rank
	// against everything seen, and earlier rows keep their colors
	want := []int{-1, 4, 4, 4, 4, 4, 4, 4, 4, 4, 0, 2, 4}
	th := NewTheme()
	th.Light = true
	themedTable(t, th, "FORCE_COLOR=3")
	buf := &bytes.Buffer{}
	var firstBatch string
	watched := func(yield func(row, error) bool) {
		for i, v := range data {
			if i == 10 {
				firstBatch = buf.String()
			}
			if !yield(v, nil) {
				return
			}
		}
	}
	assert.NoError(t, TableIter(buf, "", watched, WithColumnGreenRedScale("Value")))
	look := defaultIO.bindTheme(t.Context(), defaultIO.currentTheme(), buf, nil)
	scale := look.diverging()
	out := lines(buf.String())
	assert.Equal(t, len(data)+1, len(out))
	assert.True(t, strings.HasPrefix(buf.String(), firstBatch))
	assert.Equal(t, strings.Join(out[:11], "\n")+"\n", firstBatch)
	for i, bucket := range want {
		line, value := out[i+1], strings.TrimSpace(stripSGR(out[i+1]))
		if bucket < 0 {
			assert.Equal(t, "    "+string(look.roles.foreground)+value+FgDefault+Reset+" ", line)
			continue
		}
		assert.Contains(t, line, string(scale[bucket])+value)
	}
}

func TestThemedTableIterErrors(t *testing.T) {
	data := streamedRows()
	th := NewTheme()
	th.Funcs = template.FuncMap{"boom": func() (string, error) { return "", errors.New("boom") }}
	th.Templates.Table.Cell = `{{ if eq .Value "n10" }}{{ boom }}{{ end }}{{ .Value }}`
	themedTable(t, th)
	want := &bytes.Buffer{}
	assert.NoError(t, TableIter(want, "", iterate(data[:10])))
	firstBatch := want.String()

	// a producer failure keeps the rows already written, and drops the rest
	failing := func(yield func(themedRow, error) bool) {
		for _, v := range data[:10] {
			if !yield(v, nil) {
				return
			}
		}
		yield(themedRow{}, errors.New("gone"))
	}
	buf := &bytes.Buffer{}
	err := TableIter(buf, "", failing)
	assert.Error(t, err)
	assert.Equal(t, "row 10: fetch: gone", err.Error())
	assert.Equal(t, firstBatch, buf.String())

	// so does a Cell slot failing in a later batch
	buf.Reset()
	err = TableIter(buf, "", iterate(data))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `row 10: append: render: column "Name": theme.Templates.Table.Cell: `)
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, firstBatch, buf.String())

	err = TableIter(errWriterTable{}, "", iterate(data))
	assert.ErrorIs(t, err, io.EOF)
}
