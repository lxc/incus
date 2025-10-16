package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/internal/i18n"
)

// Table list format.
const (
	TableFormatCSV      = "csv"
	TableFormatJSON     = "json"
	TableFormatTable    = "table"
	TableFormatYAML     = "yaml"
	TableFormatCompact  = "compact"
	TableFormatMarkdown = "markdown"
)

const (
	// TableOptionNoHeader hides the table header when possible.
	TableOptionNoHeader = "noheader"

	// TableOptionHeader adds header to csv.
	TableOptionHeader = "header"
)

// RenderTable renders tabular data in various formats.
func RenderTable(w io.Writer, format string, header []string, data [][]string, raw any) error {
	fields := strings.SplitN(format, ",", 2)
	format = fields[0]

	var options []string
	if len(fields) == 2 {
		options = strings.Split(fields[1], ",")

		if slices.Contains(options, TableOptionNoHeader) {
			header = nil
		}
	}

	switch format {
	case TableFormatTable:
		table, err := getBaseTable(w, header, data)
		if err != nil {
			return err
		}

		table.Options(tablewriter.WithRendition(tw.Rendition{
			Settings: tw.Settings{
				Separators: tw.Separators{
					BetweenRows: tw.On,
				},
			},
		}))

		err = table.Render()
		if err != nil {
			return err
		}

	case TableFormatCompact:
		table, err := getBaseTable(w, header, data)
		if err != nil {
			return err
		}

		table.Options(tablewriter.WithRendition(tw.Rendition{
			Borders: tw.BorderNone,
			Settings: tw.Settings{
				Lines:      tw.LinesNone,
				Separators: tw.SeparatorsNone,
			},
		}))

		err = table.Render()
		if err != nil {
			return err
		}

	case TableFormatMarkdown:
		table, err := getBaseTable(w, header, data)
		if err != nil {
			return err
		}

		table.Options(tablewriter.WithRenderer(renderer.NewMarkdown()))

		err = table.Render()
		if err != nil {
			return err
		}

	case TableFormatCSV:
		w := csv.NewWriter(w)
		if slices.Contains(options, TableOptionHeader) {
			err := w.Write(header)
			if err != nil {
				return err
			}
		}

		err := w.WriteAll(data)
		if err != nil {
			return err
		}

		err = w.Error()
		if err != nil {
			return err
		}

	case TableFormatJSON:
		enc := json.NewEncoder(w)

		err := enc.Encode(raw)
		if err != nil {
			return err
		}

	case TableFormatYAML:
		out, err := yaml.Marshal(raw)
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(w, "%s", out)

	default:
		return fmt.Errorf(i18n.G("Invalid format %q"), format)
	}

	return nil
}

func getBaseTable(w io.Writer, header []string, data [][]string) (*tablewriter.Table, error) {
	table := tablewriter.NewTable(
		w,
		tablewriter.WithRowAlignment(tw.AlignLeft),
		tablewriter.WithRowAutoWrap(tw.WrapNone),
		tablewriter.WithHeaderAutoFormat(tw.Off),
		tablewriter.WithRendition(tw.Rendition{
			Symbols: tw.NewSymbols(tw.StyleASCII),
		}),
	)
	table.Header(header)

	err := table.Bulk(data)
	if err != nil {
		return nil, err
	}

	return table, nil
}

// Column represents a single column in a table.
type Column struct {
	Header string

	// DataFunc is a method to retrieve data for this column. The argument to this function will be an element of the
	// "data" slice that is passed into RenderSlice.
	DataFunc func(any) (string, error)
}

// ValidateFlagFormatForListOutput validates the value for the command line flag --format.
func ValidateFlagFormatForListOutput(value string) error {
	fields := strings.SplitN(value, ",", 2)
	format := fields[0]

	var options []string
	if len(fields) == 2 {
		options = strings.Split(fields[1], ",")
		for _, option := range options {
			switch option {
			case "noheader", "header", "":
			default:
				return fmt.Errorf(`Invalid modifier %q on flag "--format" (%q)`, option, value)
			}
		}
	}

	switch format {
	case TableFormatCSV, TableFormatJSON, TableFormatTable, TableFormatYAML, TableFormatCompact, TableFormatMarkdown:
	default:
		return fmt.Errorf(`Invalid value %q for flag "--format"`, format)
	}

	return nil
}
