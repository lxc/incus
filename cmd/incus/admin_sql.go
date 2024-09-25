//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	internalSQL "github.com/lxc/incus/v6/internal/sql"
)

type cmdAdminSQL struct {
	global *cmdGlobal
}

func (c *cmdAdminSQL) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("sql", i18n.G("<local|global> <query>"))
	cmd.Short = i18n.G("Execute a SQL query against the local or global database")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(`Execute a SQL query against the local or global database

  The local database is specific to the cluster member you target the
  command to, and contains member-specific data (such as the member network
  address).

  The global database is common to all members in the cluster, and contains
  cluster-specific data (such as profiles, containers, etc).

  If you are running a non-clustered server, the same applies, as that
  instance is effectively a single-member cluster.

  If <query> is the special value "-", then the query is read from
  standard input.

  If <query> is the special value ".dump", the command returns a SQL text
  dump of the given database.

  If <query> is the special value ".schema", the command returns the SQL
  text schema of the given database.

  This internal command is mostly useful for debugging and disaster
  recovery. The development team will occasionally provide hotfixes to users as a
  set of database queries to fix some data inconsistency.`))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdAdminSQL) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf(i18n.G("Missing required arguments"))
	}

	database := args[0]
	query := args[1]

	if !slices.Contains([]string{"local", "global"}, database) {
		_ = cmd.Help()

		return fmt.Errorf(i18n.G("Invalid database type"))
	}

	if query == "-" {
		// Read from stdin
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to read from stdin: %w"), err)
		}

		query = string(bytes)
	}

	// Connect to daemon
	clientArgs := incus.ConnectionArgs{
		SkipGetServer: true,
	}

	d, err := incus.ConnectIncusUnix("", &clientArgs)
	if err != nil {
		return err
	}

	if query == ".dump" || query == ".schema" {
		url := fmt.Sprintf("/internal/sql?database=%s", database)
		if query == ".schema" {
			url += "&schema=1"
		}

		response, _, err := d.RawQuery("GET", url, nil, "")
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to request dump: %w"), err)
		}

		dump := internalSQL.SQLDump{}
		err = json.Unmarshal(response.Metadata, &dump)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to parse dump response: %w"), err)
		}

		fmt.Print(dump.Text)
		return nil
	}

	data := internalSQL.SQLQuery{
		Database: database,
		Query:    query,
	}

	response, _, err := d.RawQuery("POST", "/internal/sql", data, "")
	if err != nil {
		return err
	}

	batch := internalSQL.SQLBatch{}
	err = json.Unmarshal(response.Metadata, &batch)
	if err != nil {
		return err
	}

	for i, result := range batch.Results {
		if len(batch.Results) > 1 {
			fmt.Printf(i18n.G("=> Query %d:")+"\n\n", i)
		}

		if result.Type == "select" {
			sqlPrintSelectResult(result)
		} else {
			fmt.Printf(i18n.G("Rows affected: %d")+"\n", result.RowsAffected)
		}

		if len(batch.Results) > 1 {
			fmt.Println("")
		}
	}
	return nil
}

func sqlPrintSelectResult(result internalSQL.SQLResult) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(false)
	table.SetHeader(result.Columns)
	for _, row := range result.Rows {
		data := []string{}
		for _, col := range row {
			data = append(data, fmt.Sprintf("%v", col))
		}

		table.Append(data)
	}

	table.Render()
}
