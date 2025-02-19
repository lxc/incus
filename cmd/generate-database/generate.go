package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/lxc/incus/v6/cmd/generate-database/db"
	"github.com/lxc/incus/v6/cmd/generate-database/file"
)

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
