package filter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/filter"
)

func TestParse_Error(t *testing.T) {
	cases := map[string]string{
		"not":                    "incomplete not clause",
		"foo":                    "clause has no operator",
		"not foo":                "clause has no operator",
		"foo eq":                 "clause has no value",
		"foo eq \"bar":           "unterminated quote",
		"foo eq bar and":         "unterminated compound clause",
		"foo eq \"bar egg\" and": "unterminated compound clause",
		"foo eq bar xxx":         "invalid clause composition",
	}

	for s, message := range cases {
		t.Run(s, func(t *testing.T) {
			clauses, err := filter.Parse(s, filter.QueryOperatorSet())
			assert.Nil(t, clauses)
			assert.EqualError(t, err, message)
		})
	}
}

func TestParse(t *testing.T) {
	clauses, err := filter.Parse("foo eq \"bar egg\" or not baz eq yuk", filter.QueryOperatorSet())
	require.NoError(t, err)
	assert.Len(t, clauses.Clauses, 2)
	clause1 := clauses.Clauses[0]
	clause2 := clauses.Clauses[1]
	assert.False(t, clause1.Not)
	assert.Equal(t, "and", clause1.PrevLogical)
	assert.Equal(t, "foo", clause1.Field)
	assert.Equal(t, "eq", clause1.Operator)
	assert.Equal(t, "bar egg", clause1.Value)
	assert.True(t, clause2.Not)
	assert.Equal(t, "baz", clause2.Field)
	assert.Equal(t, "or", clause2.PrevLogical)
	assert.Equal(t, "eq", clause2.Operator)
	assert.Equal(t, "yuk", clause2.Value)
}
