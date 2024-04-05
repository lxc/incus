package db_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db/generate/db"
)

func TestPackages(t *testing.T) {
	packages, err := db.Packages()
	require.NoError(t, err)

	assert.Len(t, packages, 2)

	pkg := packages["api"]
	assert.NotNil(t, pkg)

	obj := pkg.Scope.Lookup("Project")
	assert.NotNil(t, obj)
}

type Person struct {
	Name string
}

type Class struct {
	Time time.Time
	Room string
}

type Teacher struct {
	Person
	Subjects     []string
	IsSubstitute bool
	Classes      []Class
}

type TeacherFilter struct {
}

func TestParse(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "parse_test.go", nil, parser.ParseComments)
	require.NoError(t, err)

	files := map[string]*ast.File{
		"parse_test": file,
	}

	pkg, _ := ast.NewPackage(fset, files, nil, nil)

	m, err := db.Parse(pkg, "Teacher", "objects")
	require.NoError(t, err)

	assert.Equal(t, "db_test", m.Package)
	assert.Equal(t, "Teacher", m.Name)

	fields := m.Fields

	assert.Len(t, fields, 4)

	assert.Equal(t, "Name", fields[0].Name)
	assert.Equal(t, "Subjects", fields[1].Name)
	assert.Equal(t, "IsSubstitute", fields[2].Name)
	assert.Equal(t, "Classes", fields[3].Name)

	assert.Equal(t, "string", fields[0].Type.Name)
	assert.Equal(t, "[]string", fields[1].Type.Name)
	assert.Equal(t, "bool", fields[2].Type.Name)
	assert.Equal(t, "[]Class", fields[3].Type.Name)

	assert.Equal(t, db.TypeColumn, fields[0].Type.Code)
	assert.Equal(t, db.TypeSlice, fields[1].Type.Code)
	assert.Equal(t, db.TypeColumn, fields[2].Type.Code)
	assert.Equal(t, db.TypeSlice, fields[3].Type.Code)
}
