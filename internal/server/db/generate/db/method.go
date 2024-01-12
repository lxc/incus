//go:build linux && cgo && !agent

package db

import (
	"fmt"
	"go/ast"
	"go/build"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/internal/server/db/generate/file"
	"github.com/lxc/incus/internal/server/db/generate/lex"
	"github.com/lxc/incus/shared/util"
)

// Method generates a code snippet for a particular database query method.
type Method struct {
	db     string            // Target database (cluster or node)
	entity string            // Name of the database entity
	kind   string            // Kind of statement to generate
	ref    string            // ref is the current reference method for the method kind
	config map[string]string // Configuration parameters
	pkg    *ast.Package      // Package to perform for struct declaration lookup
}

// NewMethod return a new method code snippet for executing a certain mapping.
func NewMethod(database, pkg, entity, kind string, config map[string]string) (*Method, error) {
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

	if database != "" {
		database = filepath.Base(database)
	}

	method := &Method{
		db:     database,
		entity: entity,
		kind:   kind,
		config: config,
		pkg:    parsedPkg,
	}

	return method, nil
}

// Generate the desired method.
func (m *Method) Generate(buf *file.Buffer) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Unable to parse go struct %q: %w", lex.Camel(m.entity), err)
	}

	if mapping.Type != EntityTable {
		switch operation(m.kind) {
		case "GetMany":
			return m.getMany(buf)
		case "Create":
			return m.create(buf, false)
		case "Update":
			return m.update(buf)
		case "DeleteMany":
			return m.delete(buf, false)
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}
	}

	switch operation(m.kind) {
	case "GetMany":
		return m.getMany(buf)
	case "GetOne":
		return m.getOne(buf)
	case "ID":
		return m.id(buf)
	case "Exists":
		return m.exists(buf)
	case "Create":
		return m.create(buf, false)
	case "CreateOrReplace":
		return m.create(buf, true)
	case "Rename":
		return m.rename(buf)
	case "Update":
		return m.update(buf)
	case "DeleteOne":
		return m.delete(buf, true)
	case "DeleteMany":
		return m.delete(buf, false)
	default:
		return fmt.Errorf("Unknown method kind '%s'", m.kind)
	}
}

// GenerateSignature generates an interface signature for the method.
func (m *Method) GenerateSignature(buf *file.Buffer) error {
	buf.N()
	buf.L("// %sGenerated is an interface of generated methods for %s.", lex.Camel(m.entity), lex.Camel(m.entity))
	buf.L("type %sGenerated interface {", lex.Camel(m.entity))
	defer m.end(buf)
	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			m.ref = fieldName
			err := m.signature(buf, true)
			if err != nil {
				return err
			}

			m.ref = ""
			buf.N()
		}
	}

	return m.signature(buf, true)
}

func (m *Method) getMany(buf *file.Buffer) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	err = m.getManyTemplateFuncs(buf, mapping)
	if err != nil {
		return err
	}

	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			refMapping, err := Parse(m.pkg, fieldName, m.kind)
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			defer func() { _ = m.getRefs(buf, refMapping) }()
		}
	}

	// Go type name the objects to return (e.g. api.Foo).
	typ := lex.Camel(m.entity)

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("var err error")
	buf.N()
	buf.L("// Result slice.")
	buf.L("objects := make(%s, 0)", lex.Slice(typ))
	buf.N()
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		stmtVar := stmtCodeVar(m.entity, "objects")
		stmtLocal := stmtVar + "Local"
		buf.L("%s := strings.Replace(%s, \"%%s_id\", fmt.Sprintf(\"%%s_id\", parent), -1)", stmtLocal, stmtVar)
		buf.L("fillParent := make([]any, strings.Count(%s, \"%%s\"))", stmtLocal)
		buf.L("for i := range fillParent {")
		buf.L("fillParent[i] = strings.Replace(parent, \"_\", \"s_\", -1) + \"s\"")
		buf.L("}")
		buf.N()
		buf.L("queryStr := fmt.Sprintf(%s, fillParent...)", stmtLocal)
		buf.L("queryParts := strings.SplitN(queryStr, \"ORDER BY\", 2)")
		buf.L("args := []any{}")

		buf.N()
		buf.L("for i, filter := range filters {")
		buf.L("var cond string")
		buf.L("if i == 0 {")
		buf.L("cond = \" WHERE ( %%s )\"")
		buf.L("} else {")
		buf.L("cond = \" OR ( %%s )\"")
		buf.L("}")
		buf.N()
		buf.L("entries := []string{}")
		for _, filter := range mapping.Filters {
			// Skip over filter fields that are themselves filters for a referenced table.
			found := false
			for _, refField := range mapping.RefFields() {
				if filter.Type.Name == entityFilter(refField.Name) {
					found = true
					break
				}
			}

			if found {
				continue
			}

			buf.L("if filter.%s != nil {", filter.Name)
			buf.L("entries = append(entries, \"%s = ?\")", lex.Snake(filter.Name))
			buf.L("args = append(args, filter.%s)", filter.Name)
			buf.L("}")
			buf.N()
		}

		buf.L("if len(entries) == 0 {")
		buf.L("return nil, fmt.Errorf(\"Cannot filter on empty %s\")", entityFilter(mapping.Name))
		buf.L("}")
		buf.N()
		buf.L("queryParts[0] += fmt.Sprintf(cond, strings.Join(entries, \" AND \"))")
		buf.L("}")
		buf.N()
		buf.L("queryStr = strings.Join(queryParts, \" ORDER BY\")")
	} else if mapping.Type == AssociationTable {
		filter := m.config["struct"] + "ID"
		if m.db == "" {
			buf.L("sqlStmt, err := Stmt(tx, %s)", stmtCodeVar(m.entity, "objects", filter))
		} else {
			buf.L("sqlStmt, err := %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "objects", filter))
		}

		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "objects", filter)))

		buf.L("args := []any{%sID}", lex.Minuscule(m.config["struct"]))
	} else {
		filters, ignoredFilters := FiltersFromStmt(m.pkg, "objects", m.entity, mapping.Filters)
		buf.N()
		buf.L("// Pick the prepared statement and arguments to use based on active criteria.")
		buf.L("var sqlStmt *sql.Stmt")
		buf.L("args := []any{}")
		buf.L("queryParts := [2]string{}")
		buf.N()

		buf.L("if len(filters) == 0 {")
		if m.db == "" {
			buf.L("sqlStmt, err = Stmt(tx, %s)", stmtCodeVar(m.entity, "objects"))
		} else {
			buf.L("sqlStmt, err = %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "objects"))
		}

		m.ifErrNotNil(buf, false, "nil", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "objects")))
		buf.L("}")
		buf.N()
		buf.L("for i, filter := range filters {")
		for i, filter := range filters {
			branch := "if"
			if i > 0 {
				branch = "} else if"
			}

			buf.L("%s %s {", branch, activeCriteria(filter, ignoredFilters[i]))
			var args string
			for _, name := range filter {
				for _, field := range mapping.Fields {
					if name == field.Name && util.IsTrue(field.Config.Get("marshal")) {
						buf.L("marshaledFilter%s, err := query.Marshal(filter.%s)", name, name)
						m.ifErrNotNil(buf, true, "nil", "err")
						args += fmt.Sprintf("marshaledFilter%s,", name)
					} else if name == field.Name {
						args += fmt.Sprintf("filter.%s,", name)
					}
				}
			}

			buf.L("args = append(args, []any{%s}...)", args)
			buf.L("if len(filters) == 1 {")
			if m.db == "" {
				buf.L("sqlStmt, err = Stmt(tx, %s)", stmtCodeVar(m.entity, "objects", filter...))
			} else {
				buf.L("sqlStmt, err = %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "objects", filter...))
			}

			m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "objects", filter...)))
			buf.L("break")
			buf.L("}")
			buf.N()
			if m.db != "" {
				buf.L("query, err := %s.StmtString(%s)", m.db, stmtCodeVar(m.entity, "objects", filter...))
			} else {
				buf.L("query, err := StmtString(%s)", stmtCodeVar(m.entity, "objects", filter...))
			}

			m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "objects")))
			buf.L("parts := strings.SplitN(query, \"ORDER BY\", 2)")
			buf.L("if i == 0 {")
			buf.L("copy(queryParts[:], parts)")
			buf.L("continue")
			buf.L("}")
			buf.N()
			buf.L("_, where, _ := strings.Cut(parts[0], \"WHERE\")")
			buf.L("queryParts[0] += \"OR\" + where")
		}

		branch := "if"
		if len(filters) > 0 {
			branch = "} else if"
		}

		buf.L("%s %s {", branch, activeCriteria([]string{}, FieldNames(mapping.Filters)))
		buf.L("return nil, fmt.Errorf(\"Cannot filter on empty %s\")", entityFilter(mapping.Name))
		buf.L("} else {")
		buf.L("return nil, fmt.Errorf(\"No statement exists for the given Filter\")")
		buf.L("}")
		buf.L("}")
		buf.N()
	}

	if mapping.Type == EntityTable {
		buf.L("// Select.")
		buf.L("if sqlStmt != nil {")
		buf.L("objects, err = get%s(ctx, sqlStmt, args...)", lex.Plural(mapping.Name))
		buf.L("} else {")
		buf.L("queryStr := strings.Join(queryParts[:], \"ORDER BY\")")
		buf.L("objects, err = get%sRaw(ctx, tx, queryStr, args...)", lex.Plural(mapping.Name))
		buf.L("}")
		buf.N()
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", err)`, entityTable(m.entity, m.config["table"])))
	} else if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("// Select.")
		buf.L("objects, err = get%sRaw(ctx, tx, queryStr, parent, args...)", lex.Plural(mapping.Name))
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%%s_%s\" table: %%w", parent, err)`, entityTable(m.entity, m.config["table"])))
	} else {
		buf.N()
		buf.L("// Select.")
		buf.L("objects, err = get%s(ctx, sqlStmt, args...)", lex.Plural(mapping.Name))
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", err)`, entityTable(m.entity, m.config["table"])))
	}

	for _, field := range mapping.RefFields() {
		refStruct := lex.Singular(field.Name)
		refVar := lex.Minuscule(refStruct)
		refSlice := lex.Plural(refVar)
		refMapping, err := Parse(m.pkg, refStruct, "")
		if err != nil {
			return fmt.Errorf("Could not find definition for reference struct %q in package %q: %w", refStruct, m.db, err)
		}

		switch refMapping.Type {
		case EntityTable:
			assocStruct := mapping.Name + field.Name
			buf.L("%s, err := Get%s()", lex.Minuscule(assocStruct), assocStruct)
			m.ifErrNotNil(buf, true, "nil", "err")
			buf.L("for i := range objects {")
			buf.L("objects[i].%s = make([]string, 0)", field.Name)
			buf.L("refIDs, ok := %s[objects[i].ID]", lex.Minuscule(assocStruct))
			buf.L("if ok {")
			buf.L("for _, refID := range refIDs {")
			buf.L("%sURIs, err := Get%sURIs(%sFilter{ID: &refID})", refVar, refStruct, refStruct)
			m.ifErrNotNil(buf, true, "nil", "err")
			if field.Config.Get("uri") == "" {
				uriName := strings.ReplaceAll(lex.Snake(refSlice), "_", "-")
				buf.L("uris, err := urlsToResourceNames(\"/%s\", %sURIs...)", uriName, refVar)
				m.ifErrNotNil(buf, true, "nil", "err")
				buf.L("%sURIs = uris", refVar)
			}

			buf.L("objects[i].%s = append(objects[i].%s, %sURIs...)", field.Name, field.Name, refVar)
			buf.L("}")
			buf.L("}")
			buf.L("}")
		case ReferenceTable:
			buf.L("%sFilters := []%s{}", refVar, entityFilter(refStruct))
			buf.L("for _, f := range filters {")
			buf.L("filter := f.%s", refStruct)
			buf.L("if filter != nil {")
			buf.L("if %s {", activeCriteria(nil, FieldNames(refMapping.Filters)))
			buf.L("return nil, fmt.Errorf(\"Cannot filter on empty %s\")", entityFilter(refMapping.Name))
			buf.L("}")
			buf.N()
			buf.L("%sFilters = append(%sFilters, *filter)", refVar, refVar)
			buf.L("}")
			buf.L("}")
			buf.N()
			if mapping.Type == ReferenceTable {
				// A reference table should let its child reference know about its parent.
				buf.L("%s, err := Get%s(ctx, tx, parent+\"_%s\", %sFilters...)", refSlice, lex.Plural(refStruct), m.entity, refVar)
				m.ifErrNotNil(buf, true, "nil", "err")
			} else {
				buf.L("%s, err := Get%s(ctx, tx, \"%s\", %sFilters...)", refSlice, lex.Plural(refStruct), m.entity, refVar)
				m.ifErrNotNil(buf, true, "nil", "err")
			}

			buf.L("for i := range objects {")
			if field.Type.Code == TypeSlice {
				buf.L("objects[i].%s = %s[objects[i].ID]", lex.Plural(refStruct), refSlice)
			} else if field.Type.Code == TypeMap {
				buf.L("objects[i].%s = map[string]%s{}", lex.Plural(refStruct), refStruct)
				buf.L("for _, obj := range %s[objects[i].ID] {", refSlice)
				buf.L("_, ok := objects[i].%s[obj.%s]", lex.Plural(refStruct), refMapping.NaturalKey()[0].Name)
				buf.L("if !ok {")
				buf.L("objects[i].%s[obj.%s] = obj", lex.Plural(refStruct), refMapping.NaturalKey()[0].Name)
				buf.L("} else {")
				buf.L("return nil, fmt.Errorf(\"Found duplicate %s with name %%q\", obj.%s)", refStruct, refMapping.NaturalKey()[0].Name)
				buf.L("}")
				buf.L("}")
			}

			buf.L("}")
		case MapTable:
			buf.L("%sFilters := []%s{}", refVar, entityFilter(refStruct))
			buf.L("for _, f := range filters {")
			buf.L("filter := f.%s", refStruct)
			buf.L("if filter != nil {")
			buf.L("if %s {", activeCriteria(nil, FieldNames(refMapping.Filters)))
			buf.L("return nil, fmt.Errorf(\"Cannot filter on empty %s\")", entityFilter(refMapping.Name))
			buf.L("}")
			buf.N()
			buf.L("%sFilters = append(%sFilters, *filter)", refVar, refVar)
			buf.L("}")
			buf.L("}")
			buf.N()
			if mapping.Type == ReferenceTable {
				// A reference table should let its child reference know about its parent.
				buf.L("%s, err := Get%s(ctx, tx, parent+\"_%s\", %sFilters...)", refSlice, lex.Plural(refStruct), m.entity, refVar)
				m.ifErrNotNil(buf, true, "nil", "err")
			} else {
				buf.L("%s, err := Get%s(ctx, tx, \"%s\", %sFilters...)", refSlice, lex.Plural(refStruct), m.entity, refVar)
				m.ifErrNotNil(buf, true, "nil", "err")
			}

			buf.L("for i := range objects {")
			buf.L("_, ok := %s[objects[i].ID]", refSlice)
			buf.L("if !ok {")
			buf.L("objects[i].%s = map[string]string{}", refStruct)
			buf.L("} else {")
			buf.L("objects[i].%s = %s[objects[i].ID]", lex.Plural(refStruct), refSlice)
			buf.L("}")
			buf.L("}")
		}

		buf.N()
	}

	switch mapping.Type {
	case AssociationTable:
		ref := strings.Replace(mapping.Name, m.config["struct"], "", -1)
		buf.L("result := make([]%s, len(objects))", ref)
		buf.L("for i, object := range objects {")
		buf.L("%s, err := Get%s(ctx, tx, %sFilter{ID: &object.%sID})", lex.Minuscule(ref), lex.Plural(ref), ref, ref)

		m.ifErrNotNil(buf, true, "nil", "err")
		buf.L("result[i] = %s[0]", lex.Minuscule(ref))
		buf.L("}")
		buf.N()
		buf.L("return result, nil")
	case ReferenceTable:
		buf.L("resultMap := map[int][]%s{}", mapping.Name)
		buf.L("for _, object := range objects {")
		buf.L("_, ok := resultMap[object.ReferenceID]")
		buf.L("if !ok {")
		buf.L("resultMap[object.ReferenceID] = []%s{}", mapping.Name)
		buf.L("}")
		buf.N()
		buf.L("resultMap[object.ReferenceID] = append(resultMap[object.ReferenceID], object)")
		buf.L("}")
		buf.N()
		buf.L("return resultMap, nil")
	case MapTable:
		buf.L("resultMap := map[int]map[string]string{}")
		buf.L("for _, object := range objects {")
		buf.L("_, ok := resultMap[object.ReferenceID]")
		buf.L("if !ok {")
		buf.L("resultMap[object.ReferenceID] = map[string]string{}")
		buf.L("}")
		buf.N()
		buf.L("resultMap[object.ReferenceID][object.Key] = object.Value")
		buf.L("}")
		buf.N()
		buf.L("return resultMap, nil")
	case EntityTable:
		buf.L("return objects, nil")
	}

	return nil
}

func (m *Method) getRefs(buf *file.Buffer, refMapping *Mapping) error {
	m.ref = refMapping.Name
	err := m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	// reset m.ref in case m.signature is called again.
	m.ref = ""

	refStruct := refMapping.Name
	refVar := lex.Minuscule(refStruct)
	refList := lex.Plural(refVar)
	refParent := lex.Minuscule(lex.Camel(m.entity))
	refParentList := refParent + lex.Camel(refList)

	switch refMapping.Type {
	case ReferenceTable:
		buf.L("%s, err := Get%s(ctx, tx, \"%s\", filters...)", refParentList, lex.Plural(refStruct), m.entity)
		m.ifErrNotNil(buf, true, "nil", "err")
		buf.L("%s := map[string]%s{}", refList, refStruct)
		buf.L("for _, ref := range %s[%sID] {", refParentList, refParent)
		buf.L("_, ok := %s[ref.%s]", refList, refMapping.Identifier().Name)
		buf.L("if !ok {")
		buf.L("%s[ref.%s] = ref", refList, refMapping.Identifier().Name)
		buf.L("} else {")
		buf.L("return nil, fmt.Errorf(\"Found duplicate %s with name %%q\", ref.%s)", refStruct, refMapping.Identifier().Name)
		buf.L("}")
		buf.L("}")
		buf.N()
	case MapTable:
		buf.L("%s, err := Get%s(ctx, tx, \"%s\", filters...)", refParentList, lex.Plural(refStruct), m.entity)
		m.ifErrNotNil(buf, true, "nil", "err")
		buf.L("%s, ok := %s[%sID]", refList, refParentList, refParent)
		buf.L("if !ok {")
		buf.L("%s = map[string]string{}", refList)
		buf.L("}")
		buf.N()
	}

	buf.L("return %s, nil", refList)

	return nil
}

func (m *Method) getOne(buf *file.Buffer) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	nk := mapping.NaturalKey()

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("filter := %s{}", entityFilter(m.entity))
	for _, field := range nk {
		name := lex.Minuscule(field.Name)
		if name == "type" {
			name = lex.Minuscule(m.entity) + field.Name
		}

		buf.L("filter.%s = &%s", field.Name, name)
	}

	buf.N()
	buf.L("objects, err := Get%s(ctx, tx, filter)", lex.Plural(lex.Camel(m.entity)))
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%%s_%s\" table: %%w", parent, err)`, entityTable(m.entity, m.config["table"])))
	} else {
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", err)`, entityTable(m.entity, m.config["table"])))
	}

	buf.L("switch len(objects) {")
	buf.L("case 0:")
	buf.L(`        return nil, api.StatusErrorf(http.StatusNotFound, "%s not found")`, lex.Camel(m.entity))
	buf.L("case 1:")
	buf.L("        return &objects[0], nil")
	buf.L("default:")
	buf.L(`        return nil, fmt.Errorf("More than one \"%s\" entry matches")`, entityTable(m.entity, m.config["table"]))
	buf.L("}")

	return nil
}

func (m *Method) id(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := m.config["struct"]
	if !ok {
		entityCreate = lex.Camel(m.entity)
	}

	mapping, err := Parse(m.pkg, entityCreate, m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	nk := mapping.NaturalKey()

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	if m.db == "" {
		buf.L("stmt, err := Stmt(tx, %s)", stmtCodeVar(m.entity, "ID"))
	} else {
		buf.L("stmt, err := %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "ID"))
	}

	m.ifErrNotNil(buf, true, "-1", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "ID")))

	for _, field := range nk {
		if util.IsTrue(field.Config.Get("marshal")) {
			buf.L("marshaled%s, err := query.Marshal(%s)", field.Name, lex.Minuscule(field.Name))
			m.ifErrNotNil(buf, true, "-1", "err")
		}
	}

	buf.L("row := stmt.QueryRowContext(ctx, %s)", mapping.FieldParamsMarshal(nk))
	buf.L("var id int64")
	buf.L("err = row.Scan(&id)")
	buf.L("if errors.Is(err, sql.ErrNoRows) {")
	buf.L(`return -1, api.StatusErrorf(http.StatusNotFound, "%s not found")`, lex.Camel(m.entity))
	buf.L("}")
	buf.N()
	m.ifErrNotNil(buf, true, "-1", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" ID: %%w", err)`, entityTable(m.entity, m.config["table"])))
	buf.L("return id, nil")

	return nil
}

func (m *Method) exists(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := m.config["struct"]
	if !ok {
		entityCreate = lex.Camel(m.entity)
	}

	mapping, err := Parse(m.pkg, entityCreate, m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	nk := mapping.NaturalKey()

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("_, err := Get%sID(ctx, tx, %s)", lex.Camel(m.entity), mapping.FieldParams(nk))
	buf.L("if err != nil {")
	buf.L("        if api.StatusErrorCheck(err, http.StatusNotFound) {")
	buf.L("                return false, nil")
	buf.L("        }")
	buf.N()
	buf.L("        return false, err")
	buf.L("}")
	buf.N()
	buf.L("return true, nil")

	return nil
}

func (m *Method) create(buf *file.Buffer, replace bool) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			refMapping, err := Parse(m.pkg, fieldName, m.kind)
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			defer func() { _ = m.createRefs(buf, refMapping) }()
		}
	}

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	if mapping.Type == MapTable {
		buf.L("// An empty value means we are unsetting this key, so just return.")
		buf.L("if object.Value == \"\" {")
		buf.L("return nil")
		buf.L("}")
		buf.N()
	}

	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		stmtVar := stmtCodeVar(m.entity, "create")
		stmtLocal := stmtVar + "Local"
		buf.L("%s := strings.Replace(%s, \"%%s_id\", fmt.Sprintf(\"%%s_id\", parent), -1)", stmtLocal, stmtVar)
		buf.L("fillParent := make([]any, strings.Count(%s, \"%%s\"))", stmtLocal)
		buf.L("for i := range fillParent {")
		buf.L("fillParent[i] = strings.Replace(parent, \"_\", \"s_\", -1) + \"s\"")
		buf.L("}")
		buf.N()
		buf.L("queryStr := fmt.Sprintf(%s, fillParent...)", stmtLocal)
		createParams := ""
		columnFields := mapping.ColumnFields("ID")
		if mapping.Type == ReferenceTable {
			buf.L("for _, object := range objects {")
		}

		for i, field := range columnFields {
			createParams += fmt.Sprintf("object.%s", field.Name)
			if i < len(columnFields) {
				createParams += ", "
			}
		}

		refFields := mapping.RefFields()
		if len(refFields) == 0 {
			buf.L("_, err := tx.ExecContext(ctx, queryStr, %s)", createParams)
			m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Insert failed for \"%%s_%s\" table: %%w", parent, err)`, lex.Plural(m.entity)))
		} else {
			buf.L("result, err := tx.ExecContext(ctx, queryStr, %s)", createParams)
			m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Insert failed for \"%%s_%s\" table: %%w", parent, err)`, lex.Plural(m.entity)))
			buf.L("id, err := result.LastInsertId()")
			m.ifErrNotNil(buf, true, "fmt.Errorf(\"Failed to fetch ID: %w\", err)")
		}
	} else {
		nk := mapping.NaturalKey()
		nkParams := make([]string, len(nk))
		for i, field := range nk {
			nkParams[i] = fmt.Sprintf("object.%s", field.Name)
		}

		kind := "create"
		if mapping.Type != AssociationTable {
			if replace {
				kind = "create_or_replace"
			} else {
				buf.L("// Check if a %s with the same key exists.", m.entity)
				buf.L("exists, err := %sExists(ctx, tx, %s)", lex.Camel(m.entity), strings.Join(nkParams, ", "))
				m.ifErrNotNil(buf, true, "-1", "fmt.Errorf(\"Failed to check for duplicates: %w\", err)")
				buf.L("if exists {")
				buf.L(`        return -1, api.StatusErrorf(http.StatusConflict, "This \"%s\" entry already exists")`, entityTable(m.entity, m.config["table"]))
				buf.L("}")
				buf.N()
			}
		}

		if mapping.Type == AssociationTable {
			buf.L("for _, object := range objects {")
		}

		fields := mapping.ColumnFields("ID")
		buf.L("args := make([]any, %d)", len(fields))
		buf.N()

		buf.L("// Populate the statement arguments. ")
		for i, field := range fields {
			if util.IsTrue(field.Config.Get("marshal")) {
				buf.L("marshaled%s, err := query.Marshal(object.%s)", field.Name, field.Name)
				m.ifErrNotNil(buf, true, "-1", "err")
				buf.L("args[%d] = marshaled%s", i, field.Name)
			} else {
				buf.L("args[%d] = object.%s", i, field.Name)
			}
		}

		buf.N()

		buf.L("// Prepared statement to use. ")
		if m.db == "" {
			buf.L("stmt, err := Stmt(tx, %s)", stmtCodeVar(m.entity, kind))
		} else {
			buf.L("stmt, err := %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, kind))
		}

		if mapping.Type == AssociationTable {
			m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, kind)))
			buf.L("// Execute the statement. ")
			buf.L("_, err = stmt.Exec(args...)")
			m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Failed to create \"%s\" entry: %%w", err)`, entityTable(m.entity, m.config["table"])))
		} else {
			m.ifErrNotNil(buf, true, "-1", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, kind)))
			buf.L("// Execute the statement. ")
			buf.L("result, err := stmt.Exec(args...)")
			m.ifErrNotNil(buf, true, "-1", fmt.Sprintf(`fmt.Errorf("Failed to create \"%s\" entry: %%w", err)`, entityTable(m.entity, m.config["table"])))
			buf.L("id, err := result.LastInsertId()")
			m.ifErrNotNil(buf, true, "-1", fmt.Sprintf(`fmt.Errorf("Failed to fetch \"%s\" entry ID: %%w", err)`, entityTable(m.entity, m.config["table"])))
		}
	}

	for _, field := range mapping.RefFields() {
		// TODO: Remove all references to UsedBy.
		if field.Name == "UsedBy" {
			continue
		}

		refStruct := lex.Singular(field.Name)
		refMapping, err := Parse(m.pkg, lex.Singular(field.Name), "")
		if err != nil {
			return fmt.Errorf("Parse entity struct: %w", err)
		}

		switch refMapping.Type {
		case EntityTable:
			assocStruct := mapping.Name + refStruct
			buf.L("// Update association table.")
			buf.L("object.ID = int(id)")
			buf.L("err = %sUpdate%s(ctx, tx, object)", m.db, lex.Plural(assocStruct))
			m.ifErrNotNil(buf, true, "-1", fmt.Sprintf("fmt.Errorf(\"Could not update association table: %%w\", err)"))
			continue
		case ReferenceTable:
			buf.L("for _, insert := range object.%s {", field.Name)
			buf.L("insert.ReferenceID = int(id)")
		case MapTable:
			buf.L("referenceID := int(id)")
			buf.L("for key, value := range object.%s {", field.Name)
			buf.L("insert := %s{", field.Name)
			for _, ref := range refMapping.ColumnFields("ID") {
				buf.L("%s: %s,", ref.Name, lex.Minuscule(ref.Name))
			}

			buf.L("}")
			buf.N()
		}

		if mapping.Type != EntityTable {
			buf.L("err = %sCreate%s(ctx, tx, parent + \"_%s\", insert)", m.db, refStruct, m.entity)
			m.ifErrNotNil(buf, false, fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", field.Name, mapping.Name))
		} else {
			buf.L("err = %sCreate%s(ctx, tx, \"%s\", insert)", m.db, refStruct, m.entity)
			m.ifErrNotNil(buf, false, "-1", fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", field.Name, mapping.Name))
		}

		buf.L("}")
	}

	if mapping.Type == ReferenceTable || mapping.Type == AssociationTable {
		buf.L("}")
		buf.N()
		buf.L("return nil")
	} else if mapping.Type == MapTable {
		buf.L("return nil")
	} else {
		buf.L("return id, nil")
	}

	return nil
}

func (m *Method) createRefs(buf *file.Buffer, refMapping *Mapping) error {
	m.ref = refMapping.Name
	err := m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	// reset m.ref in case m.signature is called again.
	m.ref = ""

	refStruct := refMapping.Name
	refVar := lex.Minuscule(refStruct)
	refParent := lex.Minuscule(lex.Camel(m.entity))

	switch refMapping.Type {
	case ReferenceTable:
		buf.L("for key, %s := range %s {", refVar, lex.Plural(refVar))
		buf.L("%s.ReferenceID = int(%sID)", refVar, refParent)
		buf.L("%s[key] = %s", lex.Plural(refVar), refVar)
		buf.L("}")
		buf.N()
		buf.L("err := %sCreate%s(ctx, tx, \"%s\", %s)", m.db, lex.Plural(refStruct), m.entity, lex.Plural(refVar))
		m.ifErrNotNil(buf, false, fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", refStruct, lex.Camel(m.entity)))
	case MapTable:
		buf.L("referenceID := int(%sID)", refParent)
		buf.L("for key, value := range %s {", refVar)
		buf.L("insert := %s{", refStruct)
		for _, ref := range refMapping.ColumnFields("ID") {
			buf.L("%s: %s,", ref.Name, lex.Minuscule(ref.Name))
		}

		buf.L("}")
		buf.N()
		buf.L("err := %sCreate%s(ctx, tx, \"%s\", insert)", m.db, refStruct, m.entity)
		m.ifErrNotNil(buf, true, fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", refStruct, lex.Camel(m.entity)))
		buf.L("}")
	}

	buf.N()
	buf.L("return nil")

	return nil
}

func (m *Method) rename(buf *file.Buffer) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	nk := mapping.NaturalKey()

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	if m.db == "" {
		buf.L("stmt, err := Stmt(tx, %s)", stmtCodeVar(m.entity, "rename"))
	} else {
		buf.L("stmt, err := %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "rename"))
	}

	m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "rename")))

	for _, field := range nk {
		if util.IsTrue(field.Config.Get("marshal")) {
			buf.L("marshaled%s, err := query.Marshal(%s)", field.Name, lex.Minuscule(field.Name))
			m.ifErrNotNil(buf, true, "err")
		}
	}

	buf.L("result, err := stmt.Exec(to, %s)", mapping.FieldParamsMarshal(nk))
	m.ifErrNotNil(buf, true, fmt.Sprintf("fmt.Errorf(\"Rename %s failed: %%w\", err)", mapping.Name))
	buf.L("n, err := result.RowsAffected()")
	m.ifErrNotNil(buf, true, "fmt.Errorf(\"Fetch affected rows failed: %w\", err)")
	buf.L("if n != 1 {")
	buf.L("        return fmt.Errorf(\"Query affected %%d rows instead of 1\", n)")
	buf.L("}")
	buf.N()
	buf.L("return nil")

	return nil
}

func (m *Method) update(buf *file.Buffer) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	// Support using a different structure or package to pass arguments to Create.
	entityUpdate, ok := m.config["struct"]
	if !ok {
		entityUpdate = mapping.Name
	}

	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			refMapping, err := Parse(m.pkg, fieldName, m.kind)
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			defer func() { _ = m.updateRefs(buf, refMapping) }()
		}
	}

	nk := mapping.NaturalKey()

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	switch mapping.Type {
	case AssociationTable:
		ref := strings.Replace(mapping.Name, m.config["struct"], "", -1)
		refMapping, err := Parse(m.pkg, ref, "")
		if err != nil {
			return fmt.Errorf("Parse entity struct: %w", err)
		}

		refSlice := lex.Minuscule(lex.Plural(mapping.Name))
		buf.L("// Delete current entry.")
		buf.L("err := %sDelete%s%s(ctx, tx, %sID)", m.db, m.config["struct"], lex.Plural(ref), lex.Minuscule(m.config["struct"]))
		m.ifErrNotNil(buf, true, "err")
		buf.L("// Get new entry IDs.")
		buf.L("%s := make([]%s, 0, len(%s%s))", refSlice, mapping.Name, lex.Minuscule(ref), lex.Plural(refMapping.Identifier().Name))
		buf.L("for _, entry := range %s%s {", lex.Minuscule(ref), lex.Plural(refMapping.Identifier().Name))
		buf.L("refID, err := %sGet%sID(ctx, tx, entry)", m.db, ref)
		m.ifErrNotNil(buf, true, "err")
		fields := fmt.Sprintf("%sID: %sID, %sID: int(refID)", m.config["struct"], lex.Minuscule(m.config["struct"]), ref)
		buf.L("%s = append(%s, %s{%s})", refSlice, refSlice, mapping.Name, fields)
		buf.L("}")
		buf.N()
		buf.L("err = %sCreate%s%s(ctx, tx, %s)", m.db, m.config["struct"], lex.Plural(ref), refSlice)
		m.ifErrNotNil(buf, true, "err")
	case ReferenceTable:
		buf.L("// Delete current entry.")
		buf.L("err := %sDelete%s(ctx, tx, parent, referenceID)", m.db, lex.Camel(lex.Plural(m.entity)))
		m.ifErrNotNil(buf, true, "err")
		buf.L("// Insert new entries.")
		buf.L("for key, object := range %s {", lex.Plural(m.entity))
		buf.L("object.ReferenceID = referenceID")
		buf.L("%s[key] = object", lex.Plural(m.entity))
		buf.L("}")
		buf.N()
		buf.L("err = %sCreate%s(ctx, tx, parent, %s)", m.db, lex.Camel(lex.Plural(m.entity)), lex.Plural(m.entity))
		m.ifErrNotNil(buf, true, "err")
	case MapTable:
		buf.L("// Delete current entry.")
		buf.L("err := %sDelete%s(ctx, tx, parent, referenceID)", m.db, lex.Camel(lex.Plural(m.entity)))
		m.ifErrNotNil(buf, true, "err")
		buf.L("// Insert new entries.")
		buf.L("for key, value := range config {")
		buf.L("object := %s{", mapping.Name)
		for _, field := range mapping.ColumnFields("ID") {
			buf.L("%s: %s,", field.Name, lex.Minuscule(field.Name))
		}

		buf.L("}")
		buf.N()
		buf.L("err = %sCreate%s(ctx, tx, parent, object)", m.db, lex.Camel(m.entity))
		m.ifErrNotNil(buf, false, "err")
		buf.L("}")
		buf.N()
	case EntityTable:
		updateMapping, err := Parse(m.pkg, entityUpdate, m.kind)
		if err != nil {
			return fmt.Errorf("Parse entity struct: %w", err)
		}

		buf.L("id, err := Get%sID(ctx, tx, %s)", lex.Camel(m.entity), mapping.FieldParams(nk))
		m.ifErrNotNil(buf, true, "err")
		if m.db == "" {
			buf.L("stmt, err := Stmt(tx, %s)", stmtCodeVar(m.entity, "update"))
		} else {
			buf.L("stmt, err := %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "update"))
		}

		m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "update")))

		fields := updateMapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.
		params := make([]string, len(fields))
		for i, field := range fields {
			if util.IsTrue(field.Config.Get("marshal")) {
				buf.L("marshaled%s, err := query.Marshal(object.%s)", field.Name, field.Name)
				m.ifErrNotNil(buf, true, "err")
				params[i] = fmt.Sprintf("marshaled%s", field.Name)
			} else {
				params[i] = fmt.Sprintf("object.%s", field.Name)
			}
		}

		buf.L("result, err := stmt.Exec(%s)", strings.Join(params, ", ")+", id")
		m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Update \"%s\" entry failed: %%w", err)`, entityTable(m.entity, m.config["table"])))
		buf.L("n, err := result.RowsAffected()")
		m.ifErrNotNil(buf, true, "fmt.Errorf(\"Fetch affected rows: %w\", err)")
		buf.L("if n != 1 {")
		buf.L("        return fmt.Errorf(\"Query updated %%d rows instead of 1\", n)")
		buf.L("}")
		buf.N()

		for _, field := range mapping.RefFields() {
			// TODO: Eliminate UsedBy fields and move to dedicated slices for entities.
			if field.Name == "UsedBy" {
				continue
			}

			refStruct := lex.Singular(field.Name)
			refMapping, err := Parse(m.pkg, lex.Singular(field.Name), "")
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			switch refMapping.Type {
			case EntityTable:
				assocStruct := mapping.Name + refStruct
				buf.L("// Update association table.")
				buf.L("object.ID = int(id)")
				buf.L("err = %sUpdate%s(ctx, tx, object)", m.db, lex.Plural(assocStruct))
				m.ifErrNotNil(buf, true, "fmt.Errorf(\"Could not update association table: %w\", err)")
			case ReferenceTable:
				buf.L("err = %sUpdate%s(ctx, tx, \"%s\", int(id), object.%s)", m.db, lex.Singular(field.Name), m.entity, field.Name)
				m.ifErrNotNil(buf, true, fmt.Sprintf("fmt.Errorf(\"Replace %s for %s failed: %%w\", err)", field.Name, mapping.Name))
			case MapTable:
				buf.L("err = %sUpdate%s(ctx, tx, \"%s\", int(id), object.%s)", m.db, lex.Singular(field.Name), m.entity, field.Name)
				m.ifErrNotNil(buf, true, fmt.Sprintf("fmt.Errorf(\"Replace %s for %s failed: %%w\", err)", field.Name, mapping.Name))
				buf.N()
			}
		}
	}

	buf.L("return nil")

	return nil
}

func (m *Method) updateRefs(buf *file.Buffer, refMapping *Mapping) error {
	m.ref = refMapping.Name
	err := m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)

	// reset m.ref in case m.signature is called again.
	m.ref = ""

	refStruct := refMapping.Name
	refVar := lex.Minuscule(refStruct)
	refList := lex.Plural(refVar)
	refParent := lex.Minuscule(lex.Camel(m.entity))

	buf.L("err := %sUpdate%s(ctx, tx, \"%s\", int(%sID), %s)", m.db, lex.Plural(refStruct), m.entity, refParent, refList)
	m.ifErrNotNil(buf, true, fmt.Sprintf("fmt.Errorf(\"Replace %s for %s failed: %%w\", err)", refStruct, lex.Camel(m.entity)))
	buf.L("return nil")

	return nil
}

func (m *Method) delete(buf *file.Buffer, deleteOne bool) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	err = m.signature(buf, false)
	if err != nil {
		return err
	}

	defer m.end(buf)
	if mapping.Type == AssociationTable {
		if m.db == "" {
			buf.L("stmt, err := Stmt(tx, %s)", stmtCodeVar(m.entity, "delete", m.config["struct"]+"ID"))
		} else {
			buf.L("stmt, err := %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "delete", m.config["struct"]+"ID"))
		}

		m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "delete", m.config["struct"]+"ID")))
		buf.L("result, err := stmt.Exec(int(%sID))", lex.Minuscule(m.config["struct"]))
		m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Delete \"%s\" entry failed: %%w", err)`, entityTable(m.entity, m.config["table"])))
	} else if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		stmtVar := stmtCodeVar(m.entity, "delete")
		stmtLocal := stmtVar + "Local"
		buf.L("%s := strings.Replace(%s, \"%%s_id\", fmt.Sprintf(\"%%s_id\", parent), -1)", stmtLocal, stmtVar)
		buf.L("fillParent := make([]any, strings.Count(%s, \"%%s\"))", stmtLocal)
		buf.L("for i := range fillParent {")
		buf.L("fillParent[i] = strings.Replace(parent, \"_\", \"s_\", -1) + \"s\"")
		buf.L("}")
		buf.N()
		buf.L("queryStr := fmt.Sprintf(%s, fillParent...)", stmtLocal)
		buf.L("result, err := tx.ExecContext(ctx, queryStr, referenceID)")
		m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Delete entry for \"%%s_%s\" failed: %%w", parent, err)`, m.entity))
	} else {
		activeFilters := mapping.ActiveFilters(m.kind)
		if m.db == "" {
			buf.L("stmt, err := Stmt(tx, %s)", stmtCodeVar(m.entity, "delete", FieldNames(activeFilters)...))
		} else {
			buf.L("stmt, err := %s.Stmt(tx, %s)", m.db, stmtCodeVar(m.entity, "delete", FieldNames(activeFilters)...))
		}

		for _, field := range activeFilters {
			if util.IsTrue(field.Config.Get("marshal")) {
				buf.L("marshaled%s, err := query.Marshal(%s)", field.Name, lex.Minuscule(field.Name))
				m.ifErrNotNil(buf, true, "err")
			}
		}

		m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" prepared statement: %%w", err)`, stmtCodeVar(m.entity, "delete", FieldNames(activeFilters)...)))
		buf.L("result, err := stmt.Exec(%s)", mapping.FieldParamsMarshal(activeFilters))
		m.ifErrNotNil(buf, true, fmt.Sprintf(`fmt.Errorf("Delete \"%s\": %%w", err)`, entityTable(m.entity, m.config["table"])))
	}

	if deleteOne {
		buf.L("n, err := result.RowsAffected()")
	} else {
		buf.L("_, err = result.RowsAffected()")
	}

	m.ifErrNotNil(buf, true, "fmt.Errorf(\"Fetch affected rows: %w\", err)")
	if deleteOne {
		buf.L("if n == 0 {")
		buf.L(`        return api.StatusErrorf(http.StatusNotFound, "%s not found")`, lex.Camel(m.entity))
		buf.L("} else if n > 1 {")
		buf.L("        return fmt.Errorf(\"Query deleted %%d %s rows instead of 1\", n)", lex.Camel(m.entity))
		buf.L("}")
	}

	buf.N()
	buf.L("return nil")
	return nil
}

// signature generates a method or interface signature with comments, arguments, and return values.
func (m *Method) signature(buf *file.Buffer, isInterface bool) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	comment := ""
	args := "ctx context.Context, tx *sql.Tx, "
	rets := ""

	switch mapping.Type {
	case AssociationTable:
		ref := strings.Replace(mapping.Name, m.config["struct"], "", -1)
		refMapping, err := Parse(m.pkg, ref, "")
		if err != nil {
			return fmt.Errorf("Failed to parse struct %q", ref)
		}

		switch operation(m.kind) {
		case "GetMany":
			comment = fmt.Sprintf("returns all available %s for the %s.", lex.Plural(ref), m.config["struct"])
			args += fmt.Sprintf("%sID int", lex.Minuscule(m.config["struct"]))
			rets = fmt.Sprintf("([]%s, error)", ref)
		case "Create":
			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args += fmt.Sprintf("objects []%s", mapping.Name)
			rets = "error"
		case "Update":
			comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
			args += fmt.Sprintf("%sID int, %s%s []%s", lex.Minuscule(m.config["struct"]), lex.Minuscule(ref), lex.Plural(refMapping.Identifier().Name), refMapping.Identifier().Type.Name)
			rets = "error"
		case "DeleteMany":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args += fmt.Sprintf("%sID int", lex.Minuscule(m.config["struct"]))
			rets = "error"
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}

	case ReferenceTable:
		switch operation(m.kind) {
		case "GetMany":
			comment = fmt.Sprintf("returns all available %s for the parent entity.", lex.Plural(m.entity))
			args += fmt.Sprintf("parent string, filters ...%s", entityFilter(m.entity))
			rets = fmt.Sprintf("(map[int][]%s, error)", mapping.Name)
		case "Create":
			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args += fmt.Sprintf("parent string, objects map[string]%s", mapping.Name)
			rets = "error"
		case "Update":
			comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
			args += fmt.Sprintf("parent string, referenceID int, %s map[string]%s", lex.Plural(m.entity), mapping.Name)
			rets = "error"
		case "DeleteMany":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args += "parent string, referenceID int"
			rets = "error"
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}

	case MapTable:
		switch operation(m.kind) {
		case "GetMany":
			comment = fmt.Sprintf("returns all available %s.", lex.Plural(m.entity))
			args += fmt.Sprintf("parent string, filters ...%s", entityFilter(m.entity))
			rets = "(map[int]map[string]string, error)"
		case "Create":
			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args += fmt.Sprintf("parent string, object %s", mapping.Name)
			rets = "error"
		case "Update":
			comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
			args += "parent string, referenceID int, config map[string]string"
			rets = "error"
		case "DeleteMany":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args += "parent string, referenceID int"
			rets = "error"
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}

	case EntityTable:
		switch operation(m.kind) {
		case "URIs":
			comment = fmt.Sprintf("returns all available %s URIs.", m.entity)
			args += fmt.Sprintf("filter %s", entityFilter(m.entity))
			rets = "([]string, error)"
		case "GetMany":
			if m.ref == "" {
				comment = fmt.Sprintf("returns all available %s.", lex.Plural(m.entity))
				args += fmt.Sprintf("filters ...%s", entityFilter(m.entity))
				rets = fmt.Sprintf("(%s, error)", lex.Slice(lex.Camel(m.entity)))
			} else {
				comment = fmt.Sprintf("returns all available %s %s", mapping.Name, lex.Plural(m.ref))
				args += fmt.Sprintf("%sID int, filters ...%s", lex.Minuscule(mapping.Name), entityFilter(m.ref))
				refMapping, err := Parse(m.pkg, m.ref, "")
				if err != nil {
					return fmt.Errorf("Parse entity struct: %w", err)
				}

				var retType string
				switch refMapping.Type {
				case ReferenceTable:
					retType = fmt.Sprintf("map[%s]%s", refMapping.Identifier().Type.Name, refMapping.Name)
				case MapTable:
					retType = "map[string]string"
				}

				rets = fmt.Sprintf("(%s, error)", retType)
			}

		case "GetOne":
			comment = fmt.Sprintf("returns the %s with the given key.", m.entity)
			args += mapping.FieldArgs(mapping.NaturalKey())
			rets = fmt.Sprintf("(%s, error)", lex.Star(lex.Camel(m.entity)))
		case "ID":
			comment = fmt.Sprintf("return the ID of the %s with the given key.", m.entity)
			args += mapping.FieldArgs(mapping.NaturalKey())
			rets = "(int64, error)"
		case "Exists":
			comment = fmt.Sprintf("checks if a %s with the given key exists.", m.entity)
			args += mapping.FieldArgs(mapping.NaturalKey())
			rets = "(bool, error)"
		case "Create":
			if m.ref == "" {
				entityCreate, ok := m.config["struct"]
				if !ok {
					entityCreate = mapping.Name
				}

				comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
				args += fmt.Sprintf("object %s", lex.Camel(entityCreate))
				rets = "(int64, error)"
			} else {
				comment = fmt.Sprintf("adds new %s %s to the database.", m.entity, lex.Plural(m.ref))
				rets = "error"

				refMapping, err := Parse(m.pkg, m.ref, "")
				if err != nil {
					return fmt.Errorf("Parse entity struct: %w", err)
				}

				switch refMapping.Type {
				case ReferenceTable:
					args += fmt.Sprintf("%sID int64, %s map[%s]%s", lex.Minuscule(lex.Camel(m.entity)), lex.Plural(lex.Minuscule(m.ref)), refMapping.Identifier().Type.Name, m.ref)
				case MapTable:
					args += fmt.Sprintf("%sID int64, %s map[string]string", lex.Minuscule(lex.Camel(m.entity)), lex.Minuscule(m.ref))
				}
			}
		case "CreateOrReplace":
			entityCreate, ok := m.config["struct"]
			if !ok {
				entityCreate = mapping.Name
			}

			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args += fmt.Sprintf("object %s", lex.Camel(entityCreate))
			rets = "(int64, error)"
		case "Rename":
			comment = fmt.Sprintf("renames the %s matching the given key parameters.", m.entity)
			args += mapping.FieldArgs(mapping.NaturalKey(), "to string")
			rets = "error"
		case "Update":
			if m.ref == "" {
				entityUpdate, ok := m.config["struct"]
				if !ok {
					entityUpdate = mapping.Name
				}

				comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
				args += mapping.FieldArgs(mapping.NaturalKey(), fmt.Sprintf("object %s", lex.Camel(entityUpdate)))
				rets = "error"
			} else {
				comment = fmt.Sprintf("updates the %s %s matching the given key parameters.", m.entity, m.ref)
				rets = "error"

				refMapping, err := Parse(m.pkg, m.ref, "")
				if err != nil {
					return fmt.Errorf("Parse entity struct: %w", err)
				}

				switch refMapping.Type {
				case ReferenceTable:
					args += fmt.Sprintf("%sID int64, %s map[%s]%s", lex.Minuscule(lex.Camel(m.entity)), lex.Minuscule(lex.Plural(m.ref)), refMapping.Identifier().Type.Name, m.ref)
				case MapTable:
					args += fmt.Sprintf("%sID int64, %s map[string]string", lex.Minuscule(lex.Camel(m.entity)), lex.Minuscule(lex.Plural(m.ref)))
				}
			}
		case "DeleteOne":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args += mapping.FieldArgs(mapping.ActiveFilters(m.kind))
			rets = "error"
		case "DeleteMany":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args += mapping.FieldArgs(mapping.ActiveFilters(m.kind))
			rets = "error"
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}
	}

	return m.begin(buf, comment, args, rets, isInterface)
}

func (m *Method) begin(buf *file.Buffer, comment string, args string, rets string, isInterface bool) error {
	mapping, err := Parse(m.pkg, lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	name := ""
	entity := lex.Camel(m.entity)

	if mapping.Type == AssociationTable {
		parent := m.config["struct"]
		ref := strings.Replace(entity, parent, "", -1)
		switch operation(m.kind) {
		case "GetMany":
			name = fmt.Sprintf("Get%s%s", parent, lex.Plural(ref))
		case "Create":
			name = fmt.Sprintf("Create%s%s", parent, lex.Plural(ref))
		case "Update":
			name = fmt.Sprintf("Update%s%s", parent, lex.Plural(ref))
		case "DeleteMany":
			name = fmt.Sprintf("Delete%s%s", parent, lex.Plural(ref))
		}
	} else {
		entity = entity + m.ref
		switch operation(m.kind) {
		case "URIs":
			name = fmt.Sprintf("Get%sURIs", entity)
		case "GetMany":
			name = fmt.Sprintf("Get%s", lex.Plural(entity))
		case "GetOne":
			name = fmt.Sprintf("Get%s", entity)
		case "ID":
			name = fmt.Sprintf("Get%sID", entity)
		case "Exists":
			name = fmt.Sprintf("%sExists", entity)
		case "Create":
			if mapping.Type == ReferenceTable || m.ref != "" {
				entity = lex.Plural(entity)
			}

			name = fmt.Sprintf("Create%s", entity)
		case "CreateOrReplace":
			if mapping.Type == ReferenceTable || m.ref != "" {
				entity = lex.Plural(entity)
			}

			name = fmt.Sprintf("CreateOrReplace%s", entity)
		case "Rename":
			name = fmt.Sprintf("Rename%s", entity)
		case "Update":
			if mapping.Type == ReferenceTable || m.ref != "" {
				entity = lex.Plural(entity)
			}

			name = fmt.Sprintf("Update%s", entity)
		case "DeleteOne":
			name = fmt.Sprintf("Delete%s", entity)
		case "DeleteMany":
			name = fmt.Sprintf("Delete%s", lex.Plural(entity))
		default:
			name = fmt.Sprintf("%s%s", entity, m.kind)
		}
	}

	buf.L("// %s %s", name, comment)
	buf.L("// generator: %s %s", m.entity, m.kind)

	if isInterface {
		buf.L("%s(%s) %s", name, args, rets)
	} else {
		buf.L("func %s(%s) %s {", name, args, rets)
	}

	return nil
}

func (m *Method) ifErrNotNil(buf *file.Buffer, newLine bool, rets ...string) {
	buf.L("if err != nil {")
	buf.L("return %s", strings.Join(rets, ", "))
	buf.L("}")
	if newLine {
		buf.N()
	}
}

func (m *Method) end(buf *file.Buffer) {
	buf.L("}")
}

// getManyTemplateFuncs returns two functions that can be used to perform generic queries without validation, and return
// a slice of objects matching the entity. One function will accept pre-registered statements, and the other will accept
// raw queries.
func (m *Method) getManyTemplateFuncs(buf *file.Buffer, mapping *Mapping) error {
	if mapping.Type == AssociationTable {
		if m.config["struct"] != "" && strings.HasSuffix(mapping.Name, m.config["struct"]) {
			return nil
		}
	}

	tableName := mapping.TableName(m.entity, m.config["table"])
	// Create a function to get the column names to use with SELECT statements for the entity.
	buf.L("// %sColumns returns a string of column names to be used with a SELECT statement for the entity.", lex.Minuscule(mapping.Name))
	buf.L("// Use this function when building statements to retrieve database entries matching the %s entity.", mapping.Name)
	buf.L("func %sColumns() string {", lex.Minuscule(mapping.Name))
	columns := make([]string, len(mapping.Fields))
	for i, field := range mapping.Fields {
		column, err := field.SelectColumn(mapping, tableName)
		if err != nil {
			return err
		}

		columns[i] = column
	}

	buf.L("return \"%s\"", strings.Join(columns, ", "))
	buf.L("}")
	buf.N()

	// Create a function supporting prepared statements.
	buf.L("// get%s can be used to run handwritten sql.Stmts to return a slice of objects.", lex.Plural(mapping.Name))
	if mapping.Type != ReferenceTable && mapping.Type != MapTable {
		buf.L("func get%s(ctx context.Context, stmt *sql.Stmt, args ...any) ([]%s, error) {", lex.Plural(mapping.Name), mapping.Name)
	} else {
		buf.L("func get%s(ctx context.Context, stmt *sql.Stmt, parent string, args ...any) ([]%s, error) {", lex.Plural(mapping.Name), mapping.Name)
	}

	buf.L("objects := make([]%s, 0)", mapping.Name)
	buf.N()
	buf.L("dest := %s", destFunc("objects", lex.Camel(m.entity), mapping.ColumnFields()))
	buf.N()
	buf.L("err := query.SelectObjects(ctx, stmt, dest, args...)")
	if mapping.Type != ReferenceTable && mapping.Type != MapTable {
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", err)`, tableName))
	} else {
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", parent, err)`, tableName))
	}

	buf.L("	return objects, nil")
	buf.L("}")
	buf.N()

	// Create a function supporting raw queries.
	buf.L("// get%sRaw can be used to run handwritten query strings to return a slice of objects.", lex.Plural(mapping.Name))
	if mapping.Type != ReferenceTable && mapping.Type != MapTable {
		buf.L("func get%sRaw(ctx context.Context, tx *sql.Tx, sql string, args ...any) ([]%s, error) {", lex.Plural(mapping.Name), mapping.Name)
	} else {
		buf.L("func get%sRaw(ctx context.Context, tx *sql.Tx, sql string, parent string, args ...any) ([]%s, error) {", lex.Plural(mapping.Name), mapping.Name)
	}

	buf.L("objects := make([]%s, 0)", mapping.Name)
	buf.N()
	buf.L("dest := %s", destFunc("objects", lex.Camel(m.entity), mapping.ColumnFields()))
	buf.N()
	buf.L("err := query.Scan(ctx, tx, sql, dest, args...)")
	if mapping.Type != ReferenceTable && mapping.Type != MapTable {
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", err)`, tableName))
	} else {
		m.ifErrNotNil(buf, true, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", parent, err)`, tableName))
	}

	buf.L("	return objects, nil")
	buf.L("}")
	buf.N()

	return nil
}
