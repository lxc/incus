//go:build linux && cgo && !agent

package main

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
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
	var target string
	var build string
	var iface bool
	var pkg string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate database statememnts and transaction method and interface signature.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("GOPACKAGE") == "" {
				return errors.New("GOPACKAGE environment variable is not set")
			}

			if os.Getenv("GOFILE") == "" {
				return errors.New("GOFILE environment variable is not set")
			}

			return generate(target, build, iface, pkg)
		},
	}

	flags := cmd.Flags()
	flags.BoolVarP(&iface, "interface", "i", false, "create interface files")
	flags.StringVarP(&target, "target", "t", "-", "target source file to generate")
	flags.StringVarP(&build, "build", "b", "", "build comment to include")
	flags.StringVarP(&pkg, "package", "p", "", "Go package where the entity struct is declared")

	return cmd
}

const prefix = "//generate-database:mapper "

func generate(target string, build string, iface bool, pkg string) error {
	err := file.Reset(target, db.Imports, build, iface)
	if err != nil {
		return err
	}

	parsedPkg, err := packageLoad(pkg)
	if err != nil {
		return err
	}

	registeredSQLStmts := map[string]string{}
	for _, goFile := range parsedPkg.CompiledGoFiles {
		if filepath.Base(goFile) != os.Getenv("GOFILE") {
			continue
		}

		body, err := os.ReadFile(goFile)
		if err != nil {
			return err
		}

		lines := strings.Split(string(body), "\n")
		for _, line := range lines {
			// Lazy matching for prefix, does not consider Go syntax and therefore
			// lines starting with prefix, that are part of e.g. multiline strings
			// match as well. This is highly unlikely to cause false positives.
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)
				args := strings.Split(line, " ")

				command := args[0]
				entity := args[1]
				kind := args[2]
				config, err := parseParams(args[3:])
				if err != nil {
					return err
				}

				switch command {
				case "stmt":
					err = generateStmt(target, parsedPkg, entity, kind, config, registeredSQLStmts)

				case "method":
					err = generateMethod(target, iface, parsedPkg, entity, kind, config, registeredSQLStmts)

				default:
					err = fmt.Errorf("undefined command: %s", command)
				}

				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func generateStmt(target string, parsedPkg *packages.Package, entity, kind string, config map[string]string, registeredSQLStmts map[string]string) error {
	stmt, err := db.NewStmt(parsedPkg, entity, kind, config, registeredSQLStmts)
	if err != nil {
		return err
	}

	return file.Append(entity, target, stmt, false)
}

func generateMethod(target string, iface bool, parsedPkg *packages.Package, entity, kind string, config map[string]string, registeredSQLStmts map[string]string) error {
	method, err := db.NewMethod(parsedPkg, entity, kind, config, registeredSQLStmts)
	if err != nil {
		return err
	}

	return file.Append(entity, target, method, iface)
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
