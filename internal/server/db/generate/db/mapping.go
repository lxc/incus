package db

import (
	"fmt"
	"go/ast"
	"net/url"
	"slices"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/generate/lex"
	"github.com/lxc/incus/v6/shared/util"
)

// Mapping holds information for mapping database tables to a Go structure.
type Mapping struct {
	Package    string    // Package of the Go struct
	Name       string    // Name of the Go struct.
	Fields     []*Field  // Metadata about the Go struct.
	Filterable bool      // Whether the Go struct has a Filter companion struct for filtering queries.
	Filters    []*Field  // Metadata about the Go struct used for filter fields.
	Type       TableType // Type of table structure for this Go struct.

}

// TableType represents the logical type of the table defined by the Go struct.
type TableType int

// EntityTable represents the type for any entity that maps to a Go struct.
var EntityTable = TableType(0)

// ReferenceTable represents the type for for any entity that contains an
// 'entity_id' field mapping to a parent entity.
var ReferenceTable = TableType(1)

// AssociationTable represents the type for an entity that associates two
// other entities.
var AssociationTable = TableType(2)

// MapTable represents the type for a table storing key/value pairs.
var MapTable = TableType(3)

// NaturalKey returns the struct fields that can be used as natural key for
// uniquely identifying a row in the underlying table (==.
//
// By convention the natural key field is the one called "Name", unless
// specified otherwise with the `db:natural_key` tags.
func (m *Mapping) NaturalKey() []*Field {
	key := []*Field{}

	for _, field := range m.Fields {
		if field.Config.Get("primary") != "" {
			key = append(key, field)
		}
	}

	if len(key) == 0 {
		// Default primary key.
		key = append(key, m.FieldByName("Name"))
	}

	return key
}

// Identifier returns the field that uniquely identifies this entity.
func (m *Mapping) Identifier() *Field {
	for _, field := range m.NaturalKey() {
		if field.Name == "Name" || field.Name == "Fingerprint" {
			return field
		}
	}

	return nil
}

// TableName determines the table associated to the struct.
// - Individual fields may bypass this with their own `sql=<table>.<column>` tags.
// - The override `table=<name>` directive key is checked first.
// - The struct name itself is used to approximate the table name if none of the above apply.
func (m *Mapping) TableName(entity string, override string) string {
	table := entityTable(entity, override)
	if m.Type == ReferenceTable || m.Type == MapTable {
		table = "%s_" + table
	}

	return table
}

// ContainsFields checks that the mapping contains fields with the same type
// and name of given ones.
func (m *Mapping) ContainsFields(fields []*Field) bool {
	matches := map[*Field]bool{}

	for _, field := range m.Fields {
		for _, other := range fields {
			if field.Name == other.Name && field.Type.Name == other.Type.Name {
				matches[field] = true
			}
		}
	}

	return len(matches) == len(fields)
}

// FieldByName returns the field with the given name, if any.
func (m *Mapping) FieldByName(name string) *Field {
	for _, field := range m.Fields {
		if field.Name == name {
			return field
		}
	}

	return nil
}

// ActiveFilters returns the active filter fields for the kind of method.
func (m *Mapping) ActiveFilters(kind string) []*Field {
	names := activeFilters(kind)
	fields := []*Field{}
	for _, name := range names {
		field := m.FieldByName(name)
		if field != nil {
			fields = append(fields, field)
		}
	}
	return fields
}

// FieldColumnName returns the column name of the field with the given name,
// prefixed with the entity's table name.
func (m *Mapping) FieldColumnName(name string, table string) string {
	field := m.FieldByName(name)
	return fmt.Sprintf("%s.%s", table, field.Column())
}

// FilterFieldByName returns the field with the given name if that field can be
// used as query filter, an error otherwise.
func (m *Mapping) FilterFieldByName(name string) (*Field, error) {
	for _, filter := range m.Filters {
		if name == filter.Name {
			if filter.Type.Code != TypeColumn {
				return nil, fmt.Errorf("Unknown filter %q not a column", name)
			}

			return filter, nil
		}
	}

	return nil, fmt.Errorf("Unknown filter %q", name)
}

// ColumnFields returns the fields that map directly to a database column,
// either on this table or on a joined one.
func (m *Mapping) ColumnFields(exclude ...string) []*Field {
	fields := []*Field{}

	for _, field := range m.Fields {
		if slices.Contains(exclude, field.Name) {
			continue
		}

		if field.Type.Code == TypeColumn {
			fields = append(fields, field)
		}
	}

	return fields
}

// ScalarFields returns the fields that map directly to a single database
// column on another table that can be joined to this one.
func (m *Mapping) ScalarFields() []*Field {
	fields := []*Field{}

	for _, field := range m.Fields {
		if field.Config.Get("join") != "" || field.Config.Get("leftjoin") != "" {
			fields = append(fields, field)
		}
	}

	return fields
}

// RefFields returns the fields that are one-to-many references to other
// tables.
func (m *Mapping) RefFields() []*Field {
	fields := []*Field{}

	for _, field := range m.Fields {
		if field.Type.Code == TypeSlice || field.Type.Code == TypeMap {
			fields = append(fields, field)
		}
	}

	return fields
}

// FieldArgs converts the given fields to function arguments, rendering their
// name and type.
func (m *Mapping) FieldArgs(fields []*Field, extra ...string) string {
	args := []string{}

	for _, field := range fields {
		name := lex.Minuscule(field.Name)
		if name == "type" {
			name = lex.Minuscule(m.Name) + field.Name
		}

		arg := fmt.Sprintf("%s %s", name, field.Type.Name)
		args = append(args, arg)
	}

	args = append(args, extra...)

	return strings.Join(args, ", ")
}

// FieldParams converts the given fields to function parameters, rendering their
// name.
func (m *Mapping) FieldParams(fields []*Field) string {
	args := make([]string, len(fields))
	for i, field := range fields {
		name := lex.Minuscule(field.Name)
		if name == "type" {
			name = lex.Minuscule(m.Name) + field.Name
		}

		args[i] = name
	}

	return strings.Join(args, ", ")
}

// FieldParamsMarshal converts the given fields to function parameters, rendering their
// name. If the field is configured to marshal input/output, the name will be `marshaled{name}`.
func (m *Mapping) FieldParamsMarshal(fields []*Field) string {
	args := make([]string, len(fields))
	for i, field := range fields {
		name := lex.Minuscule(field.Name)
		if name == "type" {
			name = lex.Minuscule(m.Name) + field.Name
		}

		if util.IsTrue(field.Config.Get("marshal")) {
			name = fmt.Sprintf("marshaled%s", field.Name)
		}

		args[i] = name
	}

	return strings.Join(args, ", ")
}

// Field holds all information about a field in a Go struct that is relevant
// for database code generation.
type Field struct {
	Name    string
	Type    Type
	Primary bool // Whether this field is part of the natural primary key.
	Config  url.Values
}

// Stmt must be used only on a non-columnar field. It returns the name of
// statement that should be used to fetch this field. A statement with that
// name must have been generated for the entity at hand.
func (f *Field) Stmt() string {
	switch f.Name {
	case "UsedBy":
		return "used_by"
	default:
		return ""
	}
}

// IsScalar returns true if the field is a scalar column value from a joined table.
func (f *Field) IsScalar() bool {
	return f.JoinConfig() != ""
}

// IsIndirect returns true if the field is a scalar column value from a joined
// table that in turn requires another join.
func (f *Field) IsIndirect() bool {
	return f.IsScalar() && f.Config.Get("via") != ""
}

// IsPrimary returns true if the field part of the natural key.
func (f *Field) IsPrimary() bool {
	return f.Config.Get("primary") != "" || f.Name == "Name"
}

// Column returns the name of the database column the field maps to. The type
// code of the field must be TypeColumn.
func (f *Field) Column() string {
	if f.Type.Code != TypeColumn {
		panic("attempt to get column name of non-column field")
	}

	column := lex.Snake(f.Name)

	join := f.JoinConfig()
	if join != "" {
		column = fmt.Sprintf("%s AS %s", join, column)
	}

	return column
}

// SelectColumn returns a column name suitable for use with 'SELECT' statements.
// - Applies a `coalesce()` function if the 'coalesce' tag is present.
// - Returns the column in the form '<joinTable>.<joinColumn> AS <column>' if the `join` tag is present.
func (f *Field) SelectColumn(mapping *Mapping, primaryTable string) (string, error) {
	// ReferenceTable and MapTable require specific fields, so parse those instead of checking tags.
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		table := primaryTable
		column := fmt.Sprintf("%s.%s", table, lex.Snake(f.Name))
		column = strings.Replace(column, "reference", "%s", -1)

		return column, nil
	}

	tableName, columnName, err := f.SQLConfig()
	if err != nil {
		return "", err
	}

	if tableName == "" {
		tableName = primaryTable
	}

	if columnName == "" {
		columnName = lex.Snake(f.Name)
	}

	var column string
	join := f.JoinConfig()
	if join != "" {
		column = join
	} else {
		column = fmt.Sprintf("%s.%s", tableName, columnName)
	}

	coalesce, ok := f.Config["coalesce"]
	if ok {
		column = fmt.Sprintf("coalesce(%s, %s)", column, coalesce[0])
	}

	if join != "" {
		column = fmt.Sprintf("%s AS %s", column, columnName)
	}

	return column, nil
}

// OrderBy returns a column name suitable for use with the 'ORDER BY' clause.
func (f *Field) OrderBy(mapping *Mapping, primaryTable string) (string, error) {
	// ReferenceTable and MapTable require specific fields, so parse those instead of checking tags.
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		table := primaryTable
		column := fmt.Sprintf("%s.%s", table, lex.Snake(f.Name))
		column = strings.Replace(column, "reference", "%s", -1)

		return column, nil
	}

	if f.IsScalar() {
		tableName, _, err := f.ScalarTableColumn()
		if err != nil {
			return "", err
		}

		return tableName + ".id", nil
	}

	tableName, columnName, err := f.SQLConfig()
	if err != nil {
		return "", nil
	}

	if columnName == "" {
		columnName = lex.Snake(f.Name)
	}

	if tableName == "" {
		tableName = primaryTable
	}

	if tableName != "" {
		return fmt.Sprintf("%s.%s", tableName, columnName), nil
	}

	return fmt.Sprintf("%s.%s", entityTable(mapping.Name, tableName), columnName), nil
}

// JoinClause returns an SQL 'JOIN' clause using the 'join'  and 'joinon' tags, if present.
func (f *Field) JoinClause(mapping *Mapping, table string) (string, error) {
	joinTemplate := "\n  JOIN %s ON %s = %s.id"
	if f.Config.Get("join") != "" && f.Config.Get("leftjoin") != "" {
		return "", fmt.Errorf("Cannot join and leftjoin at the same time for field %q of struct %q", f.Name, mapping.Name)
	}

	join := f.JoinConfig()
	if f.Config.Get("leftjoin") != "" {
		joinTemplate = strings.Replace(joinTemplate, "JOIN", "LEFT JOIN", -1)
	}

	joinTable, _, ok := strings.Cut(join, ".")
	if !ok {
		return "", fmt.Errorf("'join' tag for field %q of struct %q must be of form <table>.<column>", f.Name, mapping.Name)
	}

	joinOn := f.Config.Get("joinon")
	if joinOn == "" {
		tableName, columnName, err := f.SQLConfig()
		if err != nil {
			return "", err
		}

		if tableName != "" && columnName != "" {
			joinOn = fmt.Sprintf("%s.%s", tableName, columnName)
		} else {
			joinOn = fmt.Sprintf("%s.%s_id", table, lex.Singular(joinTable))
		}
	}

	_, _, ok = strings.Cut(joinOn, ".")
	if !ok {
		return "", fmt.Errorf("'joinon' tag of field %q of struct %q must be of form '<table>.<column>'", f.Name, mapping.Name)
	}

	return fmt.Sprintf(joinTemplate, joinTable, joinOn, joinTable), nil
}

// InsertColumn returns a column name and parameter value suitable for an 'INSERT', 'UPDATE', or 'DELETE' statement.
// - If a 'join' tag is present, the package will be searched for the corresponding 'jointableID' registered statement
// to select the ID to insert into this table.
// - If a 'joinon' tag is present, but this table is not among the conditions, then the join will be considered indirect,
// and an empty string will be returned.
func (f *Field) InsertColumn(pkg *ast.Package, dbPkg *ast.Package, mapping *Mapping, primaryTable string) (string, string, error) {
	var column string
	var value string
	var err error
	if f.IsScalar() {
		tableName, columnName, err := f.SQLConfig()
		if err != nil {
			return "", "", err
		}

		if tableName == "" {
			tableName = primaryTable
		}

		// If there is a 'joinon' tag present without this table in the condition, then assume there is no column for this field.
		joinOn := f.Config.Get("joinon")
		if joinOn != "" {
			before, after, ok := strings.Cut(joinOn, ".")
			if !ok {
				return "", "", fmt.Errorf("'joinon' tag of field %q of struct %q must be of form '<table>.<column>'", f.Name, mapping.Name)
			}

			columnName = after
			if tableName != before {
				return "", "", nil
			}
		}

		table, _, ok := strings.Cut(f.JoinConfig(), ".")
		if !ok {
			return "", "", fmt.Errorf("'join' tag of field %q of struct %q must be of form <table>.<column>", f.Name, mapping.Name)
		}

		if columnName != "" {
			column = columnName
		} else {
			column = lex.Singular(table) + "_id"
		}

		varName := stmtCodeVar(lex.Singular(table), "ID")
		joinStmt, err := ParseStmt(pkg, dbPkg, varName)
		if err != nil {
			return "", "", fmt.Errorf("Failed to find registered statement %q for field %q of struct %q: %w", varName, f.Name, mapping.Name, err)
		}

		value = fmt.Sprintf("(%s)", strings.Replace(strings.Replace(joinStmt, "`", "", -1), "\n", "", -1))
		value = strings.Replace(value, "  ", " ", -1)
	} else {
		column, err = f.SelectColumn(mapping, primaryTable)
		if err != nil {
			return "", "", err
		}

		// Strip the table name and coalesce function if present.
		_, column, _ = strings.Cut(column, ".")
		column, _, _ = strings.Cut(column, ",")

		if mapping.Type == ReferenceTable || mapping.Type == MapTable {
			column = strings.Replace(column, "reference", "%s", -1)
		}

		value = "?"
	}

	return column, value, nil
}

func (f Field) JoinConfig() string {
	join := f.Config.Get("join")
	if join == "" {
		join = f.Config.Get("leftjoin")
	}

	return join
}

// SQLConfig returns the table and column specified by the 'sql' config key, if present.
func (f Field) SQLConfig() (string, string, error) {
	where := f.Config.Get("sql")

	if where == "" {
		return "", "", nil
	}

	table, column, ok := strings.Cut(where, ".")
	if !ok {
		return "", "", fmt.Errorf("'sql' config for field %q should be of the form <table>.<column>", f.Name)
	}

	return table, column, nil
}

// ScalarTableColumn gets the table and column from the join configuration.
func (f Field) ScalarTableColumn() (string, string, error) {
	join := f.JoinConfig()

	if join == "" {
		return "", "", fmt.Errorf("Missing join config for field %q", f.Name)
	}

	joinFields := strings.Split(join, ".")
	if len(joinFields) != 2 {
		return "", "", fmt.Errorf("Join config must be of the format <table>.<column> for field %q", f.Name)
	}

	return joinFields[0], joinFields[1], nil
}

// FieldNames returns the names of the given fields.
func FieldNames(fields []*Field) []string {
	names := []string{}
	for _, f := range fields {
		names = append(names, f.Name)
	}

	return names
}

// Type holds all information about a field in a field type that is relevant
// for database code generation.
type Type struct {
	Name string
	Code int
}

// Possible type code.
const (
	TypeColumn = iota
	TypeSlice
	TypeMap
)
