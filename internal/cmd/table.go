package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/shared/i18n"
)

// Table list format.
const (
	TableFormatCSV     = "csv"
	TableFormatJSON    = "json"
	TableFormatTable   = "table"
	TableFormatYAML    = "yaml"
	TableFormatCompact = "compact"
)

// RenderTable renders tabular data in various formats.
func RenderTable(format string, header []string, data [][]string, raw any) error {
	switch format {
	case TableFormatTable:
		table := getBaseTable(header, data)
		table.SetRowLine(true)
		table.Render()
	case TableFormatCompact:
		table := getBaseTable(header, data)
		table.SetColumnSeparator("")
		table.SetHeaderLine(false)
		table.SetBorder(false)
		table.Render()
	case TableFormatCSV:
		w := csv.NewWriter(os.Stdout)
		err := w.WriteAll(data)
		if err != nil {
			return err
		}

		err = w.Error()
		if err != nil {
			return err
		}

	case TableFormatJSON:
		enc := json.NewEncoder(os.Stdout)

		err := enc.Encode(raw)
		if err != nil {
			return err
		}

	case TableFormatYAML:
		out, err := yaml.Marshal(raw)
		if err != nil {
			return err
		}

		fmt.Printf("%s", out)
	default:
		return fmt.Errorf(i18n.G("Invalid format %q"), format)
	}

	return nil
}

func getBaseTable(header []string, data [][]string) *tablewriter.Table {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetHeader(header)
	table.AppendBulk(data)
	return table
}

// Column represents a single column in a table.
type Column struct {
	Header string

	// DataFunc is a method to retrieve data for this column. The argument to this function will be an element of the
	// "data" slice that is passed into RenderSlice.
	DataFunc func(any) (string, error)
}

// RenderSlice renders the "data" argument, which must be a slice, into a table or as json/yaml as defined by the
// "format" argument. The "columns" argument defines which columns will be rendered. It will error if the data argument
// is not a slice, if the format is unrecognized, if any characters in the columns argument is not present in the
// columnMap argument.
func RenderSlice(data any, format string, displayColumns string, sortColumns string, columnMap map[rune]Column) error {
	rows, err := anyToSlice(data)
	if err != nil {
		return fmt.Errorf("Cannot render table: %w", err)
	}

	switch format {
	case TableFormatTable, TableFormatCSV, TableFormatCompact:
		break
	case TableFormatJSON, TableFormatYAML:
		return RenderTable(format, nil, nil, data)
	default:
		return fmt.Errorf(i18n.G("Invalid format %q"), format)
	}

	headers := make([]string, len(displayColumns))
	for i, r := range displayColumns {
		column, ok := columnMap[r]
		if !ok {
			return fmt.Errorf("Invalid column %q", string(r))
		}

		headers[i] = column.Header
	}

	tableData := make([][]string, len(rows))
	for i, row := range rows {
		rowData := make([]string, len(displayColumns))
		for j, r := range displayColumns {
			rowData[j], err = columnMap[r].DataFunc(row)
			if err != nil {
				return err
			}
		}

		tableData[i] = rowData
	}

	err = SortByPrecedence(tableData, displayColumns, sortColumns)
	if err != nil {
		return nil
	}

	return RenderTable(format, headers, tableData, data)
}

// anyToSlice converts the given any to a []any. It will error if the underlying type is not a slice.
func anyToSlice(slice any) ([]any, error) {
	s := reflect.ValueOf(slice)
	if s.Kind() != reflect.Slice {
		return nil, fmt.Errorf("Provided argument is not a slice")
	}

	// Keep the distinction between nil and empty slice input
	if s.IsNil() {
		return nil, nil
	}

	ret := make([]interface{}, s.Len())

	for i := 0; i < s.Len(); i++ {
		ret[i] = s.Index(i).Interface()
	}

	return ret, nil
}
