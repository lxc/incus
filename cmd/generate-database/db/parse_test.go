package db_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/lxc/incus/v6/cmd/generate-database/db"
)

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

type TeacherFilter struct{}

func TestParse(t *testing.T) {
	pkg, err := packages.Load(&packages.Config{
		Mode:  packages.LoadTypes | packages.NeedTypesInfo,
		Tests: true,
	}, "")
	require.NoError(t, err)

	m, err := db.Parse(pkg[1].Types, "Teacher", "objects")
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
