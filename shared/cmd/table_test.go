package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/cyphar/incus/shared/api"
)

type tableSuite struct {
	suite.Suite
}

func TestTableSuite(t *testing.T) {
	suite.Run(t, new(tableSuite))
}

func (s *tableSuite) TestRenderSlice() {
	type TestDataType struct {
		SomeString  string   `json:"some_string" yaml:"some_string"`
		SomeInteger int      `json:"some_integer" yaml:"some_integer"`
		SomeURL     *api.URL `json:"some_url" yaml:"some_url"`
	}

	testDataTypeColumnMap := map[rune]Column{
		's': {
			Header: "Some String",
			DataFunc: func(a any) (string, error) {
				return a.(TestDataType).SomeString, nil
			},
		},
		'i': {
			Header: "Some Integer",
			DataFunc: func(a any) (string, error) {
				return strconv.Itoa(a.(TestDataType).SomeInteger), nil
			},
		},
		'u': {
			Header: "Some URL",
			DataFunc: func(a any) (string, error) {
				return a.(TestDataType).SomeURL.String(), nil
			},
		},
	}

	testDataTypeSlice := []TestDataType{
		{
			SomeString:  "foo",
			SomeInteger: 1,
			SomeURL:     api.NewURL().Path("1.0", "instances", "foo"),
		},
		{
			SomeString:  "fizz",
			SomeInteger: 3,
			SomeURL:     api.NewURL().Path("1.0", "instances", "fizz"),
		},
		{
			SomeString:  "buzz",
			SomeInteger: 4,
			SomeURL:     api.NewURL().Path("1.0", "instances", "buzz"),
		},
		{
			SomeString:  "bar",
			SomeInteger: 2,
			SomeURL:     api.NewURL().Path("1.0", "instances", "bar"),
		},
	}

	type args struct {
		data           any
		format         string
		displayColumns string
		sortColumns    string
		columnMap      map[rune]Column
	}

	tests := []struct {
		name      string
		args      args
		expect    string
		expectErr error
	}{
		{
			name: "Incorrect data type (must be slice)",
			args: args{
				data: TestDataType{
					SomeString:  "hello",
					SomeInteger: 3,
					SomeURL:     api.NewURL().Host("example.com"),
				},
				format: TableFormatCSV,
			},
			expect:    "",
			expectErr: fmt.Errorf("Cannot render table: %w", fmt.Errorf("Provided argument is not a slice")),
		},
		{
			name: "Invalid format",
			args: args{
				data:   testDataTypeSlice,
				format: "not a table format",
			},
			expect:    "",
			expectErr: fmt.Errorf("Invalid format \"not a table format\""),
		},
		{
			name: "happy path - csv, display all, sort precedence string->integer->url",
			args: args{
				data:           testDataTypeSlice,
				format:         TableFormatCSV,
				displayColumns: "siu",
				sortColumns:    "siu",
				columnMap:      testDataTypeColumnMap,
			},
			expect: `bar,2,/1.0/instances/bar
buzz,4,/1.0/instances/buzz
fizz,3,/1.0/instances/fizz
foo,1,/1.0/instances/foo
`,
			expectErr: nil,
		},
		{
			name: "happy path - compact, display string+integer, sort by integer",
			args: args{
				data:           testDataTypeSlice,
				format:         TableFormatCompact,
				displayColumns: "si",
				sortColumns:    "i",
				columnMap:      testDataTypeColumnMap,
			},
			expect: `  SOME STRING  SOME INTEGER  ` + `
  foo          1             ` + `
  bar          2             ` + `
  fizz         3             ` + `
  buzz         4             ` + `
`,
			expectErr: nil,
		},
		{
			name: "happy path - table, display all, do not sort",
			args: args{
				data:           testDataTypeSlice,
				format:         TableFormatTable,
				displayColumns: "siu",
				sortColumns:    "",
				columnMap:      testDataTypeColumnMap,
			},
			expect: `+-------------+--------------+---------------------+
| SOME STRING | SOME INTEGER |      SOME URL       |
+-------------+--------------+---------------------+
| foo         | 1            | /1.0/instances/foo  |
+-------------+--------------+---------------------+
| fizz        | 3            | /1.0/instances/fizz |
+-------------+--------------+---------------------+
| buzz        | 4            | /1.0/instances/buzz |
+-------------+--------------+---------------------+
| bar         | 2            | /1.0/instances/bar  |
+-------------+--------------+---------------------+
`,
			expectErr: nil,
		},
	}

	for i, test := range tests {
		s.T().Logf("Test %d: %s", i, test.name)

		// Set up a pipe to read from stdout.
		stdout := os.Stdout
		r, w, err := os.Pipe()
		s.Require().NoError(err)
		os.Stdout = w

		// Call method but fix stdout before making any assertions.
		actualErr := RenderSlice(test.args.data, test.args.format, test.args.displayColumns, test.args.sortColumns, test.args.columnMap)

		// Restore stdout and close the writer now so that io.Copy gets an io.EOF and doesn't block indefinitely.
		os.Stdout = stdout
		err = w.Close()
		s.Require().NoError(err)

		// Read what was printed to stdout.
		buffer := bytes.NewBuffer(nil)
		_, err = io.Copy(buffer, r)
		s.Require().NoError(err)
		output := buffer.String()

		// Make assertions
		s.Equal(test.expectErr, actualErr)
		s.Equal(test.expect, output)
	}
}
