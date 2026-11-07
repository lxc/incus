package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// scanGoFiles collects every msgid reachable from the given sources.
//
// A source may be a directory, a single .go file or a shell-style glob. An
// empty list walks the module root. Paths recorded in locations are relative
// to root, which is what "#: cmd/incus/info.go:459" means in the template.
func scanGoFiles(root string, sources []string) (map[string]*msgInfo, error) {
	infos := make(map[string]*msgInfo)

	files, err := collectGoFiles(root, sources)
	if err != nil {
		return nil, err
	}

	for _, path := range files {
		scanGoFile(root, path, infos)
	}

	return infos, nil
}

// collectGoFiles resolves sources, or the whole module, to .go file paths.
func collectGoFiles(root string, sources []string) ([]string, error) {
	var files []string
	seen := make(map[string]bool)

	add := func(path string) {
		if !strings.HasSuffix(path, ".go") || seen[path] {
			return
		}

		seen[path] = true
		files = append(files, path)
	}

	walk := func(dir string) error {
		return filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if fi.IsDir() {
				name := fi.Name()
				if name != "." && (name == ".git" || name == "vendor" || name == "node_modules" ||
					strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}

				return nil
			}

			add(path)
			return nil
		})
	}

	if len(sources) == 0 {
		err := walk(root)
		if err != nil {
			return nil, err
		}

		return files, nil
	}

	for _, src := range sources {
		pattern := src
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(root, pattern)
		}

		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}

		if len(matches) == 0 {
			return nil, fmt.Errorf("source %q matches no files", src)
		}

		for _, m := range matches {
			fi, err := os.Stat(m)
			if err != nil {
				return nil, err
			}

			if fi.IsDir() {
				err := walk(m)
				if err != nil {
					return nil, err
				}

				continue
			}

			add(m)
		}
	}

	return files, nil
}

// scanGoFile records every i18n call found in one file.
func scanGoFile(root, path string, infos map[string]*msgInfo) {
	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return // build tags, generated code, ...
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}

	rel = filepath.ToSlash(rel)

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		shape, ok := classifyI18nCall(call)
		if !ok || len(call.Args) == 0 {
			return true
		}

		msgid, err := extractStringLiteral(call.Args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"Warning: %s: i18n argument is not a constant string: %v\n",
				fset.Position(call.Args[0].Pos()), err)
			return true
		}

		if msgid == "" {
			return true
		}

		info := infos[msgid]
		if info == nil {
			info = &msgInfo{}
			infos[msgid] = info
		}

		// Locations are appended in the order the AST walk finds them. The
		// template's location list is not sorted: xgettext appends them as it
		// goes, and a sorted list would not match what translators already
		// have.
		info.addLocation(fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line))

		plural := ""
		if shape == i18nPlural && len(call.Args) >= 2 {
			p, err := extractStringLiteral(call.Args[1])
			if err == nil {
				plural = p
			}
		}

		if plural != "" && info.Plural == "" {
			info.Plural = plural
		}

		if (needsCFormat(msgid) || needsCFormat(plural)) && !hasFlag(info.Flags, "c-format") {
			info.Flags = append(info.Flags, "c-format")
		}

		return true
	})
}

// i18nCallShape distinguishes the supported call shapes.
type i18nCallShape int

const (
	i18nNone i18nCallShape = iota
	i18nSingular
	i18nPlural
)

// classifyI18nCall classifies call as i18n.G(...) or i18n.NG(...).
func classifyI18nCall(call *ast.CallExpr) (i18nCallShape, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return i18nNone, false
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "i18n" {
		return i18nNone, false
	}

	switch sel.Sel.Name {
	case "G":
		return i18nSingular, true
	case "NG":
		return i18nPlural, true
	default:
		return i18nNone, false
	}
}

// hasFlag reports whether flags already contains want.
func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}

	return false
}

// extractStringLiteral returns the value of a constant string expression:
// a string literal, or a '+' concatenation of them.
func extractStringLiteral(expr ast.Expr) (string, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return extractBasicLit(e)
	case *ast.BinaryExpr:
		return extractBinaryExpr(e)
	default:
		return "", fmt.Errorf("unsupported expression type %T", expr)
	}
}

// extractBasicLit extracts a single string literal value.
func extractBasicLit(lit *ast.BasicLit) (string, error) {
	if lit.Kind != token.STRING {
		return "", fmt.Errorf("not a string literal")
	}

	return strconv.Unquote(lit.Value)
}

// extractBinaryExpr extracts a '+' concatenation of string literals.
func extractBinaryExpr(expr *ast.BinaryExpr) (string, error) {
	if expr.Op != token.ADD {
		return "", fmt.Errorf("unsupported binary operator %v (only + is supported)", expr.Op)
	}

	left, err := extractStringLiteral(expr.X)
	if err != nil {
		return "", err
	}

	right, err := extractStringLiteral(expr.Y)
	if err != nil {
		return "", err
	}

	return left + right, nil
}
