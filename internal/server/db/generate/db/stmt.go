//go:build linux && cgo && !agent

package db

import (
	"fmt"
	"go/ast"
	"go/build"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/generate/file"
	"github.com/lxc/incus/v6/internal/server/db/generate/lex"
)

// Stmt generates a particular database query statement.
type Stmt struct {
	db     string            // Database package receiver
	dbPkg  *ast.Package      // Package where database related helpers are located.
	entity string            // Name of the database entity
	kind   string            // Kind of statement to generate
	config map[string]string // Configuration parameters
	pkg    *ast.Package      // Package to perform for struct declaration lookups
}

// NewStmt return a new statement code snippet for running the given kind of
// query against the given database entity.
func NewStmt(database, pkg, entity, kind string, config map[string]string) (*Stmt, error) {
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

	parsedPkg, err := ParsePackage(pkgPath)
	if err != nil {
		return nil, err
	}

	var dbPkg *ast.Package
	if database != "" {
		importPkg, err := build.Import(database, "", build.FindOnly)
		if err != nil {
			return nil, fmt.Errorf("Invalid import path %q: %w", database, err)
		}

		dbPkg, err = ParsePackage(importPkg.Dir)
		if err != nil {
			return nil, err
		}

		database = filepath.Base(database)
	}

	stmt := &Stmt{
		db:     database,
		dbPkg:  dbPkg,
		entity: entity,
		kind:   kind,
		config: config,
		pkg:    parsedPkg,
	}

	return stmt, nil
}

// Generate plumbing and wiring code for the desired statement.
func (s *Stmt) Generate(buf *file.Buffer) error {
	kind := strings.Split(s.kind, "-by-")[0]

	switch kind {
	case "objects":
		return s.objects(buf)
	case "delete":
		return s.delete(buf)
	case "create":
		return s.create(buf, false)
	case "create-or-replace":
		return s.create(buf, true)
	case "id":
		return s.id(buf)
	case "rename":
		return s.rename(buf)
	case "update":
		return s.update(buf)
	default:
		return fmt.Errorf("Unknown statement '%s'", s.kind)
	}
}

// GenerateSignature is not used for statements.
func (s *Stmt) GenerateSignature(buf *file.Buffer) error {
	return nil
}

func (s *Stmt) objects(buf *file.Buffer) error {
	if strings.HasPrefix(s.kind, "objects-by") {
		return s.objectsBy(buf)
	}

	mapping, err := Parse(s.pkg, lex.Camel(s.entity), s.kind)
	if err != nil {
		return err
	}

	table := mapping.TableName(s.entity, s.config["table"])
	boiler := stmts["objects"]
	fields := mapping.ColumnFields()
	columns := make([]string, len(fields))
	for i, field := range fields {
		column, err := field.SelectColumn(mapping, table)
		if err != nil {
			return err
		}

		columns[i] = column
	}

	orderBy := []string{}
	orderByFields := []*Field{}
	for _, field := range fields {
		if field.Config.Get("order") != "" {
			orderByFields = append(orderByFields, field)
		}
	}

	if len(orderByFields) < 1 {
		orderByFields = mapping.NaturalKey()
	}

	for _, field := range orderByFields {
		column, err := field.OrderBy(mapping, table)
		if err != nil {
			return err
		}

		orderBy = append(orderBy, column)
	}

	joinFields := mapping.ScalarFields()
	joins := make([]string, 0, len(joinFields))
	for _, field := range joinFields {
		join, err := field.JoinClause(mapping, table)
		if err != nil {
			return err
		}

		joins = append(joins, join)
	}

	table += strings.Join(joins, "")
	sql := fmt.Sprintf(boiler, strings.Join(columns, ", "), table, strings.Join(orderBy, ", "))
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("const %s = `%s`", stmtName, sql)
	} else {
		s.register(buf, stmtName, sql)
	}

	return nil
}

// objectsBy parses the variable declaration produced by the 'objects' function, and appends a WHERE clause to its SQL
// string using the objects-by-<FIELD> field suffixes, and then creates a new variable declaration.
// Strictly, it will look for variables of the form 'var <entity>Objects = <database>.RegisterStmt(`SQL String`)'.
func (s *Stmt) objectsBy(buf *file.Buffer) error {
	mapping, err := Parse(s.pkg, lex.Camel(s.entity), s.kind)
	if err != nil {
		return err
	}

	where := []string{}
	filters := strings.Split(s.kind[len("objects-by-"):], "-and-")
	sqlString, err := ParseStmt(s.pkg, s.dbPkg, stmtCodeVar(s.entity, "objects"))
	if err != nil {
		return err
	}

	queryParts := strings.SplitN(sqlString, "ORDER BY", 2)
	joinStr := " JOIN"
	if strings.Contains(queryParts[0], " LEFT JOIN") {
		joinStr = " LEFT JOIN"
	}

	preJoin, _, _ := strings.Cut(queryParts[0], joinStr)
	_, tableName, _ := strings.Cut(preJoin, "FROM ")
	tableName, _, _ = strings.Cut(tableName, "\n")

	for _, filter := range filters {
		field, err := mapping.FilterFieldByName(filter)
		if err != nil {
			return err
		}

		table, columnName, err := field.SQLConfig()
		if err != nil {
			return err
		}

		var column string
		if table != "" && columnName != "" {
			if field.IsScalar() {
				column = columnName
			} else {
				column = table + "." + columnName
			}
		} else if field.IsScalar() {
			column = lex.Snake(field.Name)
		} else {
			column = mapping.FieldColumnName(field.Name, tableName)
		}

		coalesce, ok := field.Config["coalesce"]
		if ok {
			// Ensure filters operate on the coalesced value for fields using coalesce setting.
			where = append(where, fmt.Sprintf("coalesce(%s, %s) = ? ", column, coalesce[0]))
		} else {
			where = append(where, fmt.Sprintf("%s = ? ", column))
		}
	}

	queryParts[0] = fmt.Sprintf("%sWHERE ( %s)", queryParts[0], strings.Join(where, "AND "))
	sqlString = strings.Join(queryParts, "\n  ORDER BY")
	s.register(buf, stmtCodeVar(s.entity, "objects", filters...), sqlString)

	return nil
}

func (s *Stmt) create(buf *file.Buffer, replace bool) error {
	entityCreate := lex.Camel(s.entity)

	mapping, err := Parse(s.pkg, entityCreate, s.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	table := mapping.TableName(s.entity, s.config["table"])
	all := mapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.
	columns := make([]string, 0, len(all))
	values := make([]string, 0, len(all))

	for _, field := range all {
		column, value, err := field.InsertColumn(s.pkg, s.dbPkg, mapping, table)
		if err != nil {
			return err
		}

		if column == "" && value == "" {
			continue
		}

		columns = append(columns, column)
		values = append(values, value)
	}

	tmpl := stmts[s.kind]
	if replace {
		tmpl = stmts["replace"]
	}

	sql := fmt.Sprintf(tmpl, table, strings.Join(columns, ", "), strings.Join(values, ", "))
	kind := strings.Replace(s.kind, "-", "_", -2)
	stmtName := stmtCodeVar(s.entity, kind)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("const %s = `%s`", stmtName, sql)
	} else {
		s.register(buf, stmtName, sql)
	}

	return nil
}

func (s *Stmt) id(buf *file.Buffer) error {
	mapping, err := Parse(s.pkg, lex.Camel(s.entity), s.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	table := mapping.TableName(s.entity, s.config["table"])
	nk := mapping.NaturalKey()
	where := make([]string, 0, len(nk))
	joins := make([]string, 0, len(nk))
	for _, field := range nk {
		tableName, columnName, err := field.SQLConfig()
		if err != nil {
			return err
		}

		var column string
		if field.IsScalar() {
			column = field.JoinConfig()

			join, err := field.JoinClause(mapping, table)
			joins = append(joins, join)
			if err != nil {
				return err
			}
		} else if tableName != "" && columnName != "" {
			column = tableName + "." + columnName
		} else {
			column = mapping.FieldColumnName(field.Name, table)
		}

		where = append(where, fmt.Sprintf("%s = ?", column))
	}

	sql := fmt.Sprintf(stmts[s.kind], table, table+strings.Join(joins, ""), strings.Join(where, " AND "))
	stmtName := stmtCodeVar(s.entity, "ID")
	s.register(buf, stmtName, sql)

	return nil
}

func (s *Stmt) rename(buf *file.Buffer) error {
	mapping, err := Parse(s.pkg, lex.Camel(s.entity), s.kind)
	if err != nil {
		return err
	}

	table := mapping.TableName(s.entity, s.config["table"])
	nk := mapping.NaturalKey()
	updates := make([]string, 0, len(nk))
	for _, field := range nk {
		column, value, err := field.InsertColumn(s.pkg, s.dbPkg, mapping, table)
		if err != nil {
			return err
		}

		if column == "" && value == "" {
			continue
		}

		updates = append(updates, fmt.Sprintf("%s = %s", column, value))
	}

	sql := fmt.Sprintf(stmts[s.kind], table, strings.Join(updates, " AND "))
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	s.register(buf, stmtName, sql)
	return nil
}

func (s *Stmt) update(buf *file.Buffer) error {
	entityUpdate := lex.Camel(s.entity)

	mapping, err := Parse(s.pkg, entityUpdate, s.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	table := mapping.TableName(s.entity, s.config["table"])
	all := mapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.
	updates := make([]string, 0, len(all))
	for _, field := range all {
		column, value, err := field.InsertColumn(s.pkg, s.dbPkg, mapping, table)
		if err != nil {
			return err
		}

		if column == "" && value == "" {
			continue
		}

		updates = append(updates, fmt.Sprintf("%s = %s", column, value))
	}

	sql := fmt.Sprintf(stmts[s.kind], table, strings.Join(updates, ", "), "id = ?")
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	s.register(buf, stmtName, sql)

	return nil
}

func (s *Stmt) delete(buf *file.Buffer) error {
	mapping, err := Parse(s.pkg, lex.Camel(s.entity), s.kind)
	if err != nil {
		return err
	}

	table := mapping.TableName(s.entity, s.config["table"])
	var where string
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		where = "%s_id = ?"
	}

	if strings.HasPrefix(s.kind, "delete-by") {
		filters := strings.Split(s.kind[len("delete-by-"):], "-and-")
		conditions := make([]string, 0, len(filters))
		for _, filter := range filters {
			field, err := mapping.FilterFieldByName(filter)
			if err != nil {
				return err
			}

			column, value, err := field.InsertColumn(s.pkg, s.dbPkg, mapping, table)
			if err != nil {
				return err
			}

			if column == "" && value == "" {
				continue
			}

			conditions = append(conditions, fmt.Sprintf("%s = %s", column, value))
		}

		where = strings.Join(conditions, " AND ")
	}

	sql := fmt.Sprintf(stmts["delete"], table, where)
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("const %s = `%s`", stmtName, sql)
	} else {
		s.register(buf, stmtName, sql)
	}

	return nil
}

// Output a line of code that registers the given statement and declares the
// associated statement code global variable.
func (s *Stmt) register(buf *file.Buffer, stmtName, sql string, filters ...string) {
	if !strings.HasPrefix(sql, "`") || !strings.HasSuffix(sql, "`") {
		sql = fmt.Sprintf("`\n%s\n`", sql)
	}

	if s.db != "" {
		buf.L("var %s = %s.RegisterStmt(%s)", stmtName, s.db, sql)
	} else {
		buf.L("var %s = RegisterStmt(%s)", stmtName, sql)
	}
}

// Map of boilerplate statements.
var stmts = map[string]string{
	"objects": "SELECT %s\n  FROM %s\n  ORDER BY %s",
	"create":  "INSERT INTO %s (%s)\n  VALUES (%s)",
	"replace": "INSERT OR REPLACE INTO %s (%s)\n VALUES (%s)",
	"id":      "SELECT %s.id FROM %s\n  WHERE %s",
	"rename":  "UPDATE %s SET name = ? WHERE %s",
	"update":  "UPDATE %s\n  SET %s\n WHERE %s",
	"delete":  "DELETE FROM %s WHERE %s",
}
