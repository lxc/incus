package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// extractMessages collects the i18n.G and i18n.NG arguments found in the Go files of the given directories.
func extractMessages(dirs []string) (map[string]*message, error) {
	messages := map[string]*message{}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}

			err = extractFile(filepath.Join(dir, entry.Name()), messages)
			if err != nil {
				return nil, err
			}
		}
	}

	return messages, nil
}

// extractFile records the i18n calls of one file.
func extractFile(path string, messages map[string]*message) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		plural, ok := i18nCall(call)
		if !ok {
			return true
		}

		msgid, err := constantString(call.Args[0])
		if err != nil {
			fmt.Printf("Warning: %s: %v\n", fset.Position(call.Args[0].Pos()), err)
			return true
		}

		if msgid == "" {
			return true
		}

		msg := messages[msgid]
		if msg == nil {
			msg = &message{}
			messages[msgid] = msg
		}

		location := fmt.Sprintf("%s:%d", path, fset.Position(call.Pos()).Line)
		if !slices.Contains(msg.locations, location) {
			msg.locations = append(msg.locations, location)
		}

		if plural && len(call.Args) > 1 && msg.plural == "" {
			msg.plural, _ = constantString(call.Args[1])
		}

		if isFormat(msgid) || isFormat(msg.plural) {
			msg.format = true
		}

		return true
	})

	return nil
}

// i18nCall reports whether the call is i18n.G or i18n.NG, and whether it is the plural form.
func i18nCall(call *ast.CallExpr) (bool, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false, false
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "i18n" {
		return false, false
	}

	switch sel.Sel.Name {
	case "G":
		return false, true
	case "NG":
		return true, true
	}

	return false, false
}

// constantString returns the value of a string literal or a concatenation of string literals.
func constantString(expr ast.Expr) (string, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", fmt.Errorf("Not a string literal")
		}

		return strconv.Unquote(e.Value)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", fmt.Errorf("Unsupported operator %q", e.Op)
		}

		left, err := constantString(e.X)
		if err != nil {
			return "", err
		}

		right, err := constantString(e.Y)
		if err != nil {
			return "", err
		}

		return left + right, nil
	}

	return "", fmt.Errorf("Unsupported expression %T", expr)
}

// isFormat reports whether the string holds a printf verb, a doubled percent sign doesn't count.
func isFormat(s string) bool {
	return strings.Contains(strings.ReplaceAll(s, "%%", ""), "%")
}
