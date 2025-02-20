//go:build linux && cgo && !agent

package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"go/build"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/tools/go/packages"

	"github.com/lxc/incus/v6/cmd/generate-database/db"
	"github.com/lxc/incus/v6/cmd/generate-database/file"
	"github.com/lxc/incus/v6/cmd/generate-database/lex"
)

// Return a new db command.
func newDb() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db [sub-command]",
		Short: "Database-related code generation.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("Not implemented")
		},
	}

	cmd.AddCommand(newDbSchema())
	cmd.AddCommand(newDbMapper())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

func newDbSchema() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Generate database schema by applying updates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return db.UpdateSchema()
		},
	}

	return cmd
}

func newDbMapper() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mapper [sub-command]",
		Short: "Generate code mapping database rows to Go structs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("Not implemented")
		},
	}

	cmd.AddCommand(newDbMapperGenerate())

	return cmd
}

func newDbMapperGenerate() *cobra.Command {
	var pkg string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate database statememnts and transaction method and interface signature.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("GOPACKAGE") == "" {
				return errors.New("GOPACKAGE environment variable is not set")
			}

			return generate(pkg)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&pkg, "package", "p", "", "Go package where the entity struct is declared")

	return cmd
}

const prefix = "//generate-database:mapper "

func generate(pkg string) error {
	parsedPkg, err := packageLoad(pkg)
	if err != nil {
		return err
	}

	registeredSQLStmts := map[string]string{}
	for _, goFile := range parsedPkg.CompiledGoFiles {
		body, err := os.ReadFile(goFile)
		if err != nil {
			return err
		}

		// Reset target to stdout
		target := "-"

		lines := strings.Split(string(body), "\n")
		for _, line := range lines {
			// Lazy matching for prefix, does not consider Go syntax and therefore
			// lines starting with prefix, that are part of e.g. multiline strings
			// match as well. This is highly unlikely to cause false positives.
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)

				// Use csv parser to properly handle arguments surrounded by double quotes.
				r := csv.NewReader(strings.NewReader(line))
				r.Comma = ' ' // space
				args, err := r.Read()
				if err != nil {
					return err
				}

				if len(args) == 0 {
					return fmt.Errorf("command missing")
				}

				command := args[0]

				switch command {
				case "target":
					if len(args) != 2 {
						return fmt.Errorf("invalid arguments for command target, one argument for the target filename: %s", line)
					}

					target = args[1]
				case "reset":
					err = commandReset(args[1:], target)

				case "stmt":
					err = commandStmt(args[1:], target, parsedPkg, registeredSQLStmts)

				case "method":
					err = commandMethod(args[1:], target, parsedPkg, registeredSQLStmts)

				default:
					err = fmt.Errorf("unknown command: %s", command)
				}

				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func commandReset(commandLine []string, target string) error {
	var err error

	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	iface := flags.BoolP("interface", "i", false, "create interface files")
	buildComment := flags.StringP("build", "b", "", "build comment to include")

	err = flags.Parse(commandLine)
	if err != nil {
		return err
	}

	err = file.Reset(target, db.Imports, *buildComment, *iface)
	if err != nil {
		return err
	}

	return nil
}

func commandStmt(commandLine []string, target string, parsedPkg *packages.Package, registeredSQLStmts map[string]string) error {
	var err error

	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	entity := flags.StringP("entity", "e", "", "database entity to generate the statement for")

	err = flags.Parse(commandLine)
	if err != nil {
		return err
	}

	if len(flags.Args()) < 1 {
		return fmt.Errorf("argument <kind> missing for stmt command")
	}

	kind := flags.Arg(0)
	config, err := parseParams(flags.Args()[1:])
	if err != nil {
		return err
	}

	stmt, err := db.NewStmt(parsedPkg, *entity, kind, config, registeredSQLStmts)
	if err != nil {
		return err
	}

	return file.Append(*entity, target, stmt, false)
}

func commandMethod(commandLine []string, target string, parsedPkg *packages.Package, registeredSQLStmts map[string]string) error {
	var err error

	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	iface := flags.BoolP("interface", "i", false, "create interface files")
	entity := flags.StringP("entity", "e", "", "database entity to generate the method for")

	err = flags.Parse(commandLine)
	if err != nil {
		return err
	}

	if len(flags.Args()) < 1 {
		return fmt.Errorf("argument <kind> missing for method command")
	}

	kind := flags.Arg(0)
	config, err := parseParams(flags.Args()[1:])
	if err != nil {
		return err
	}

	method, err := db.NewMethod(parsedPkg, *entity, kind, config, registeredSQLStmts)
	if err != nil {
		return err
	}

	return file.Append(*entity, target, method, *iface)
}

func packageLoad(pkg string) (*packages.Package, error) {
	var pkgPath string
	if pkg != "" {
		importPkg, err := build.Import(pkg, "", build.FindOnly)
		if err != nil {
			return nil, fmt.Errorf("Invalid import path %q: %w", pkg, err)
		}

		pkgPath = importPkg.Dir
	} else {
		var err error
		pkgPath, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	parsedPkg, err := packages.Load(&packages.Config{
		Mode: packages.LoadTypes | packages.NeedTypesInfo,
	}, pkgPath)
	if err != nil {
		return nil, err
	}

	return parsedPkg[0], nil
}

func parseParams(args []string) (map[string]string, error) {
	config := map[string]string{}
	for _, arg := range args {
		key, value, err := lex.KeyValue(arg)
		if err != nil {
			return nil, fmt.Errorf("Invalid config parameter: %w", err)
		}

		config[key] = value
	}

	return config, nil
}
