package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	cliColor "github.com/lxc/incus/v7/cmd/incus/color"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type listColumn[T any] struct {
	header      string
	description string
	handler     func(T) string
}

type listHook[T any] func(string) (listColumn[T], bool, error)

type listable[T any] struct {
	global         *cmdGlobal
	flagFormat     string
	flagColumns    string
	defaultColumns string
	columns        map[rune]listColumn[T]
	cmd            *cobra.Command
	hooks          []listHook[T]
	activeColumns  []listColumn[T]
	activeRunes    string
	parsed         bool
}

func alphaRank(c rune) int {
	switch {
	case 'a' <= c && c <= 'z':
		return int(c-'a') * 2
	case 'A' <= c && c <= 'Z':
		return int(c-'A')*2 + 1
	case '0' <= c && c <= '9':
		return int(c-'0') - 10
	}

	return 52
}

func alphaSort(r1 rune, r2 rune) int {
	rank1 := alphaRank(r1)
	rank2 := alphaRank(r2)
	if rank1 < rank2 {
		return -1
	}

	if rank1 == rank2 {
		return 0
	}

	return 1
}

type columnToggle struct {
	column    rune
	condition string
	prepend   bool
	unmet     error
}

var remoteToggle = columnToggle{
	column:    'R',
	condition: i18n.G("with --all-remotes"),
	prepend:   true,
}

var projectToggle = columnToggle{
	column:    'e',
	condition: i18n.G("with --all-projects"),
	prepend:   true,
}

var locationToggle = columnToggle{
	column:    'L',
	condition: i18n.G("when clustered"),
	unmet:     errors.New(i18n.G("Can't specify column L when querying a single standalone server")),
}

func (l *listable[T]) init(cmd *cobra.Command, defaultColumns string, columns map[rune]listColumn[T], toggles ...columnToggle) {
	l.defaultColumns = defaultColumns
	l.columns = columns
	l.cmd = cmd

	cli.AddStringFlag(cmd.Flags(), &l.flagFormat, "format|f", l.global.defaultListFormat(), "", i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable them if missing, e.g. csv,header`))
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		err := cli.ValidateFlagFormatForListOutput(l.flagFormat)
		if err != nil {
			return err
		}

		return l.parseColumns()
	}

	if len(columns) == 1 {
		l.flagColumns = defaultColumns
		return
	}

	cli.AddStringFlag(cmd.Flags(), &l.flagColumns, "columns|c", defaultColumns, "", i18n.G("Columns"))
	columnColors := map[rune]*color.Color{}
	for _, column := range defaultColumns {
		columnColors[column] = color.New(color.FgGreen)
	}

	defaultLayout := color.New(color.FgGreen, color.Bold).Sprint(defaultColumns)
	for _, toggle := range toggles {
		var c *color.Color
		if toggle.prepend {
			c = color.New(color.FgYellow)
			defaultLayout = c.Sprintf(i18n.G("[%s, %s]"), toggle.condition, color.New(color.Bold).Sprint(string(toggle.column))) + defaultLayout
		} else {
			c = color.New(color.FgCyan)
			defaultLayout = defaultLayout + c.Sprintf(i18n.G("[%s %s]"), color.New(color.Bold).Sprint(string(toggle.column)), toggle.condition)
		}

		columnColors[toggle.column] = c
	}

	var b strings.Builder
	b.WriteString(cmd.Long)
	b.WriteString(cliColor.CommandHeader(i18n.G("Columns")))
	b.WriteString(" ")
	fmt.Fprintf(&b, i18n.G("(default layout: %s)"), defaultLayout)
	b.WriteString("\n  ")
	b.WriteString(i18n.G("The -c option takes a list of columns that control which attributes to output when displaying a table (table or csv format)."))
	b.WriteString("\n  ")
	b.WriteString(i18n.G("The columns can be specified with consecutive shorthand characters (optionally separated with commas), among the following:"))
	for _, shorthand := range slices.SortedFunc(maps.Keys(columns), alphaSort) {
		b.WriteString("\n    ")
		c, ok := columnColors[shorthand]
		if !ok {
			c = color.New()
		}

		c.Add(color.Bold)

		fmt.Fprintf(&b, i18n.G("%s: %s"), c.Sprint(string(shorthand)), columns[shorthand].description)
	}

	cmd.Long = b.String()
}

// preCheck allows to run checks before columns are parsed. This is the only moment when columns can
// reliably be adjusted.
func (l *listable[T]) preCheck(check func() error) {
	pre := l.cmd.PreRunE
	l.cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		err := check()
		if err != nil {
			return err
		}

		return pre(cmd, args)
	}
}

// parseColumns parses the columns flag and populates both a slice of columns in use and a string of
// built-in columns in use. After calling this, the value of the columns flag must not be relied
// upon anymore.
func (l *listable[T]) parseColumns() error {
out:
	for _, columnEntry := range strings.Split(l.flagColumns, ",") {
		if columnEntry == "" {
			return fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing comma) in '%s'"), l.flagColumns)
		}

		for _, hook := range l.hooks {
			column, ok, err := hook(columnEntry)
			if ok {
				l.activeColumns = append(l.activeColumns, column)
				continue out
			}

			if err != nil {
				return err
			}
		}

		for _, columnRune := range columnEntry {
			column, ok := l.columns[columnRune]
			if !ok {
				return fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			l.activeColumns = append(l.activeColumns, column)
			l.activeRunes = l.activeRunes + string(columnRune)
		}
	}

	l.parsed = true
	return nil
}

// buildTableData constructs the table ready to sort and display.
func (l *listable[T]) buildTableData(entries []T) ([]string, [][]string, error) {
	data := [][]string{}
	for _, entry := range entries {
		line := []string{}
		for _, column := range l.activeColumns {
			line = append(line, column.handler(entry))
		}

		data = append(data, line)
	}

	header := []string{}
	for _, column := range l.activeColumns {
		header = append(header, column.header)
	}

	return header, data, nil
}

func (l *listable[T]) run(entries []T, rawEntries ...any) error {
	header, data, err := l.buildTableData(entries)
	if err != nil {
		return err
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	raw := any(entries)
	if len(rawEntries) > 0 {
		raw = rawEntries[0]
	}

	return cli.RenderTable(os.Stdout, l.flagFormat, header, data, raw)
}

func (l *listable[T]) adjustDefaultColumns(columns string) error {
	if l.parsed {
		// This is a programming error, which thus doesn’t need translating.
		return errors.New("Cannot adjust default columns after parsing")
	}

	if !l.columnsSpecified() {
		l.flagColumns = columns
	}

	return nil
}

func (l *listable[T]) addHook(hook listHook[T], description string) {
	var b strings.Builder
	if len(l.hooks) == 0 {
		b.WriteString("\n\n")
		b.WriteString(cliColor.CommandHeader(i18n.G("Custom columns")))
		b.WriteString(" ")
		b.WriteString(i18n.G("(separated with commas)"))
	}

	l.hooks = append(l.hooks, hook)
	b.WriteString("\n  ")
	b.WriteString(description)
	l.cmd.Long = l.cmd.Long + b.String()
}

func yesNo(b bool) string {
	if b {
		return i18n.G("YES")
	}

	return i18n.G("NO")
}

func (l *listable[T]) columnsSpecified() bool {
	return l.cmd.Flags().Changed("columns")
}

func (l *listable[T]) hasColumn(column rune) bool {
	return strings.ContainsRune(l.activeRunes, column)
}

func (l *listable[T]) hasAnyColumn(columns string) bool {
	return strings.ContainsAny(l.activeRunes, columns)
}

// toggle adjusts default columns depending on whether a condition is met. If the condition is not
// met, it checks whether an error should be returned if the column was manually specified.
func (l *listable[T]) toggle(t columnToggle, conditionMet bool) error {
	if conditionMet {
		column, ok := l.columns[t.column]
		if !ok {
			// This is a programming error, which thus doesn’t need translating.
			return fmt.Errorf("Unknown column %c", t.column)
		}

		if l.columnsSpecified() {
			return nil
		}

		if t.prepend {
			l.activeColumns = append([]listColumn[T]{column}, l.activeColumns...)
			l.activeRunes = string(t.column) + l.activeRunes
		} else {
			l.activeColumns = append(l.activeColumns, column)
			l.activeRunes = l.activeRunes + string(t.column)
		}
	} else if t.unmet != nil && l.hasColumn(t.column) {
		return t.unmet
	}

	return nil
}
