package db

import (
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/generate/lex"
	"github.com/lxc/incus/v6/shared/util"
)

// Return the table name for the given database entity.
func entityTable(entity string, override string) string {
	if override != "" {
		return override
	}

	entityParts := strings.Split(lex.Snake(entity), "_")
	tableParts := make([]string, len(entityParts))
	for i, part := range entityParts {
		if strings.HasSuffix(part, "ty") || strings.HasSuffix(part, "ly") {
			tableParts[i] = part
		} else {
			tableParts[i] = lex.Plural(part)
		}
	}

	return strings.Join(tableParts, "_")
}

// Return the name of the Filter struct for the given database entity.
func entityFilter(entity string) string {
	return fmt.Sprintf("%sFilter", lex.Camel(entity))
}

// Return the name of the global variable holding the registration code for
// the given kind of statement aganst the given entity.
func stmtCodeVar(entity string, kind string, filters ...string) string {
	prefix := lex.Minuscule(lex.Camel(entity))
	name := fmt.Sprintf("%s%s", prefix, lex.Camel(kind))

	if len(filters) > 0 {
		name += "By"
		name += strings.Join(filters, "And")
	}

	return name
}

// operation returns the kind of operation being performed, without filter fields.
func operation(kind string) string {
	return strings.Split(kind, "-by-")[0]
}

// activeFilters returns the filters mentioned in the command name.
func activeFilters(kind string) []string {
	startIndex := strings.Index(kind, "-by-") + len("-by-")
	return strings.Split(kind[startIndex:], "-and-")
}

// Return an expression evaluating if a filter should be used (based on active
// criteria).
func activeCriteria(filter []string, ignoredFilter []string) string {
	expr := ""
	for i, name := range filter {
		if i > 0 {
			expr += " && "
		}

		expr += fmt.Sprintf("filter.%s != nil", name)
	}

	for _, name := range ignoredFilter {
		if len(expr) > 0 {
			expr += " && "
		}

		expr += fmt.Sprintf("filter.%s == nil", name)
	}

	return expr
}

// Return the code for a "dest" function, to be passed as parameter to
// query.SelectObjects in order to scan a single row.
func destFunc(slice string, typ string, fields []*Field) string {
	var builder strings.Builder
	writeLine := func(line string) { builder.WriteString(fmt.Sprintf("%s\n", line)) }

	writeLine(`func(scan func(dest ...any) error) error {`)

	varName := lex.Minuscule(string(typ[0]))
	writeLine(fmt.Sprintf("%s := %s{}", varName, typ))

	checkErr := func() {
		writeLine("if err != nil {\nreturn err\n}")
		writeLine("")
	}

	unmarshal := func(declVarName string, field *Field) {
		writeLine(fmt.Sprintf("err = query.Unmarshal(%s, &%s.%s)", declVarName, varName, field.Name))
		checkErr()
	}

	args := make([]string, len(fields))
	declVars := make(map[string]*Field, len(fields))
	declVarNames := make([]string, 0, len(fields))
	for i, field := range fields {
		var arg string
		if util.IsTrue(field.Config.Get("marshal")) {
			declVarName := fmt.Sprintf("%sStr", lex.Minuscule(field.Name))
			declVarNames = append(declVarNames, declVarName)
			declVars[declVarName] = field
			arg = fmt.Sprintf("&%s", declVarName)
		} else {
			arg = fmt.Sprintf("&%s.%s", varName, field.Name)
		}

		args[i] = arg
	}

	for _, declVarName := range declVarNames {
		writeLine(fmt.Sprintf("var %s string", declVarName))
	}

	writeLine(fmt.Sprintf("err := scan(%s)", strings.Join(args, ", ")))
	checkErr()
	for _, declVarName := range declVarNames {
		unmarshal(declVarName, declVars[declVarName])
	}

	writeLine(fmt.Sprintf("%s = append(%s, %s)\n", slice, slice, varName))
	writeLine("return nil")
	writeLine("}")

	return builder.String()
}
