//go:build linux && cgo && !agent

package db

import (
	"fmt"
	"go/ast"
	"go/types"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/cmd/generate-database/lex"
)

// FiltersFromStmt parses all filtering statement defined for the given entity. It
// returns all supported combinations of filters, sorted by number of criteria, and
// the corresponding set of unused filters from the Filter struct.
func FiltersFromStmt(pkg *types.Package, kind string, entity string, filters []*Field, registeredSQLStmts map[string]string) ([][]string, [][]string) {
	objects := pkg.Scope().Names()
	stmtFilters := [][]string{}

	prefix := fmt.Sprintf("%s%sBy", lex.Minuscule(lex.Camel(entity)), lex.Camel(kind))

	seenNames := make(map[string]struct{}, len(objects))

	for _, name := range objects {
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		rest := name[len(prefix):]
		stmtFilters = append(stmtFilters, strings.Split(rest, "And"))
		seenNames[rest] = struct{}{}
	}

	for name := range registeredSQLStmts {
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		rest := name[len(prefix):]

		_, ok := seenNames[rest]
		if ok {
			continue
		}

		stmtFilters = append(stmtFilters, strings.Split(rest, "And"))
	}

	stmtFilters = sortFilters(stmtFilters)
	ignoredFilters := [][]string{}

	for _, filterGroup := range stmtFilters {
		ignoredFilterGroup := []string{}
		for _, filter := range filters {
			if !slices.Contains(filterGroup, filter.Name) {
				ignoredFilterGroup = append(ignoredFilterGroup, filter.Name)
			}
		}
		ignoredFilters = append(ignoredFilters, ignoredFilterGroup)
	}

	return stmtFilters, ignoredFilters
}

// RefFiltersFromStmt parses all filtering statement defined for the given entity reference.
func RefFiltersFromStmt(pkg *types.Package, entity string, ref string, filters []*Field, registeredSQLStmts map[string]string) ([][]string, [][]string) {
	objects := pkg.Scope().Names()
	stmtFilters := [][]string{}

	prefix := fmt.Sprintf("%s%sRefBy", lex.Minuscule(lex.Camel(entity)), lex.Capital(ref))

	seenNames := make(map[string]struct{}, len(objects))

	for _, name := range objects {
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		rest := name[len(prefix):]
		stmtFilters = append(stmtFilters, strings.Split(rest, "And"))
		seenNames[rest] = struct{}{}
	}

	for name := range registeredSQLStmts {
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		rest := name[len(prefix):]

		_, ok := seenNames[rest]
		if ok {
			continue
		}

		stmtFilters = append(stmtFilters, strings.Split(rest, "And"))
	}

	stmtFilters = sortFilters(stmtFilters)
	ignoredFilters := [][]string{}

	for _, filterGroup := range stmtFilters {
		ignoredFilterGroup := []string{}
		for _, filter := range filters {
			if !slices.Contains(filterGroup, filter.Name) {
				ignoredFilterGroup = append(ignoredFilterGroup, filter.Name)
			}
		}
		ignoredFilters = append(ignoredFilters, ignoredFilterGroup)
	}

	return stmtFilters, ignoredFilters
}

func sortFilters(filters [][]string) [][]string {
	sort.Slice(filters, func(i, j int) bool {
		n1 := len(filters[i])
		n2 := len(filters[j])
		if n1 != n2 {
			return n1 > n2
		}

		f1 := sortFilter(filters[i])
		f2 := sortFilter(filters[j])
		for k := range f1 {
			if f1[k] == f2[k] {
				continue
			}

			return f1[k] > f2[k]
		}

		panic("duplicate filter")
	})
	return filters
}

func sortFilter(filter []string) []string {
	f := make([]string, len(filter))
	copy(f, filter)
	sort.Sort(sort.Reverse(sort.StringSlice(f)))
	return f
}

// Parse the structure declaration with the given name found in the given Go package.
// Any 'Entity' struct should also have an 'EntityFilter' struct defined in the same file.
func Parse(pkg *types.Package, name string, kind string) (*Mapping, error) {
	// The main entity struct.
	str := findStruct(pkg.Scope(), name)
	if str == nil {
		return nil, fmt.Errorf("No declaration found for %q", name)
	}

	fields, err := parseStruct(str, kind, pkg.Name())
	if err != nil {
		return nil, fmt.Errorf("Failed to parse %q: %w", name, err)
	}

	m := &Mapping{
		Package:    pkg.Name(),
		Name:       name,
		Fields:     fields,
		Type:       tableType(pkg, name, fields),
		Filterable: true,
	}

	if m.Filterable {
		// The 'EntityFilter' struct. This is used for filtering on specific fields of the entity.
		filterName := name + "Filter"
		filterStr := findStruct(pkg.Scope(), filterName)
		if filterStr == nil {
			return nil, fmt.Errorf("No declaration found for %q", filterName)
		}

		filters, err := parseStruct(filterStr, kind, pkg.Name())
		if err != nil {
			return nil, fmt.Errorf("Failed to parse %q: %w", name, err)
		}

		for i, filter := range filters {
			// Any field in EntityFilter must be present in the original struct.
			field := m.FieldByName(filter.Name)
			if field == nil {
				return nil, fmt.Errorf("Filter field %q is not in struct %q", filter.Name, name)
			}

			// Assign the config tags from the main entity struct to the Filter struct.
			filters[i].Config = field.Config

			// A Filter field and its indirect references must all be in the Filter struct.
			if field.IsIndirect() {
				indirectField := lex.Camel(field.Config.Get("via"))
				for i, f := range filters {
					if f.Name == indirectField {
						break
					}

					if i == len(filters)-1 {
						return nil, fmt.Errorf("Field %q requires field %q in struct %q", field.Name, indirectField, name+"Filter")
					}
				}
			}
		}

		m.Filters = filters
	}

	return m, nil
}

// ParseStmt returns the SQL string passed as an argument to a variable declaration of a call to RegisterStmt with the given name.
// e.g. the SELECT string from 'var instanceObjects = RegisterStmt(`SELECT * from instances...`)'.
func ParseStmt(name string, defs map[*ast.Ident]types.Object, registeredSQLStmts map[string]string) (string, error) {
	sql, ok := registeredSQLStmts[name]
	if ok {
		return sql, nil
	}

	for stmtVar := range defs {
		if stmtVar.Name != name {
			continue
		}

		spec, ok := stmtVar.Obj.Decl.(*ast.ValueSpec)
		if !ok {
			continue
		}

		if len(spec.Values) != 1 {
			continue
		}

		expr, ok := spec.Values[0].(*ast.CallExpr)
		if !ok {
			continue
		}

		if len(expr.Args) != 1 {
			continue
		}

		lit, ok := expr.Args[0].(*ast.BasicLit)
		if !ok {
			continue
		}

		return lit.Value, nil
	}

	return "", fmt.Errorf("Declaration for %q not found", name)
}

// tableType determines the TableType for the given struct fields.
func tableType(pkg *types.Package, name string, fields []*Field) TableType {
	fieldNames := FieldNames(fields)
	entities := strings.Split(lex.Snake(name), "_")
	if len(entities) == 2 {
		struct1 := findStruct(pkg.Scope(), lex.Camel(lex.Singular(entities[0])))
		struct2 := findStruct(pkg.Scope(), lex.Camel(lex.Singular(entities[1])))
		if struct1 != nil && struct2 != nil {
			return AssociationTable
		}
	}

	if slices.Contains(fieldNames, "ReferenceID") {
		if slices.Contains(fieldNames, "Key") && slices.Contains(fieldNames, "Value") {
			return MapTable
		}

		return ReferenceTable
	}

	return EntityTable
}

// Find the StructType node for the structure with the given name.
func findStruct(scope *types.Scope, name string) *types.Struct {
	obj := scope.Lookup(name)
	if obj == nil {
		return nil
	}

	typ, ok := obj.(*types.TypeName)
	if !ok {
		return nil
	}

	str, ok := typ.Type().Underlying().(*types.Struct)
	if !ok {
		return nil
	}

	return str
}

// Extract field information from the given structure.
func parseStruct(str *types.Struct, kind string, pkgName string) ([]*Field, error) {
	fields := make([]*Field, 0)

	for i := 0; i < str.NumFields(); i++ {
		f := str.Field(i)
		if f.Embedded() {
			// Check if this is a parent struct.
			parentStr, ok := f.Type().Underlying().(*types.Struct)
			if !ok {
				continue
			}

			parentFields, err := parseStruct(parentStr, kind, pkgName)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse parent struct: %w", err)
			}

			fields = append(fields, parentFields...)

			continue
		}

		field, err := parseField(f, str.Tag(i), kind, pkgName)
		if err != nil {
			return nil, err
		}

		// Don't add field if it has been ignored.
		if field != nil {
			fields = append(fields, field)
		}
	}

	return fields, nil
}

func parseField(f *types.Var, structTag string, kind string, pkgName string) (*Field, error) {
	name := f.Name()

	if !f.Exported() {
		return nil, fmt.Errorf("Unexported field name %q", name)
	}

	// Ignore fields that are marked with a tag of `db:"ingore"`
	if structTag != "" {
		tagValue := reflect.StructTag(structTag).Get("db")
		if tagValue == "ignore" {
			return nil, nil
		}
	}

	typeName := parseType(f.Type(), pkgName)
	if typeName == "" {
		return nil, fmt.Errorf("Unsupported type for field %q", name)
	}

	typeObj := Type{
		Name: typeName,
	}

	typeObj.Code = TypeColumn
	if strings.HasPrefix(typeName, "[]") {
		typeObj.Code = TypeSlice
	} else if strings.HasPrefix(typeName, "map[") {
		typeObj.Code = TypeMap
	}

	var config url.Values
	if structTag != "" {
		var err error
		config, err = url.ParseQuery(reflect.StructTag(structTag).Get("db"))
		if err != nil {
			return nil, fmt.Errorf("Parse 'db' structure tag: %w", err)
		}
	}

	// Ignore fields that are marked with `db:"omit"`.
	omit := config.Get("omit")
	if omit != "" {
		omitFields := strings.Split(omit, ",")
		stmtKind := strings.Replace(lex.Snake(kind), "_", "-", -1)
		switch kind {
		case "URIs":
			stmtKind = "names"
		case "GetMany":
			stmtKind = "objects"
		case "GetOne":
			stmtKind = "objects"
		case "DeleteMany":
			stmtKind = "delete"
		case "DeleteOne":
			stmtKind = "delete"
		}

		if slices.Contains(omitFields, kind) || slices.Contains(omitFields, stmtKind) {
			return nil, nil
		} else if kind == "exists" && slices.Contains(omitFields, "id") {
			// Exists checks ID, so if we are omitting the field from ID, also omit it from Exists.
			return nil, nil
		}
	}

	field := Field{
		Name:   name,
		Type:   typeObj,
		Config: config,
	}

	return &field, nil
}

func parseType(x types.Type, pkgName string) string {
	switch t := x.(type) {
	case *types.Pointer:
		return parseType(t.Elem(), pkgName)
	case *types.Slice:
		return "[]" + parseType(t.Elem(), pkgName)
	case *types.Basic:
		s := t.String()
		if s == "byte" {
			return "uint8"
		}

		return s
	case *types.Array:
		return "[" + strconv.FormatInt(t.Len(), 10) + "]" + parseType(t.Elem(), pkgName)
	case *types.Map:
		return "map[" + t.Key().String() + "]" + parseType(t.Elem(), pkgName)
	case *types.Named:
		if pkgName == t.Obj().Pkg().Name() {
			return t.Obj().Name()
		}

		return t.Obj().Pkg().Name() + "." + t.Obj().Name()
	case nil:
		return ""
	default:
		return ""
	}
}
