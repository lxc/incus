package lex

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	internalUtil "github.com/lxc/incus/v6/internal/util"
)

// Parse runs the Go parser against the given package directory.
func Parse(dir string) (*ast.Package, error) {
	if !internalUtil.IsDir(dir) {
		return nil, fmt.Errorf("Package directory does not exist %q", dir)
	}

	fset := token.NewFileSet()

	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("Search source file: %w", err)
	}

	files := map[string]*ast.File{}
	for _, path := range paths {
		// Skip test files.
		if strings.Contains(path, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("Parse Go source file %q", path)
		}

		files[path] = file
	}

	// Ignore errors because they are typically about unresolved symbols.
	pkg, _ := ast.NewPackage(fset, files, nil, nil)

	return pkg, nil
}
