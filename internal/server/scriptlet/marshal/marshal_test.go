package marshal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.starlark.net/starlark"
)

type starlarkMarshalTest struct {
	from      any
	to        starlark.Value
	errPrefix string
}

type LowerStruct struct {
	Config map[string]string `json:"config"`
}

type MiddleStruct struct {
	LowerStruct
}

type TopStruct struct {
	MiddleStruct
}

type DummyStringer int

var _ fmt.Stringer = DummyStringer(0)

func (DummyStringer) String() string { return "(DummyStringer)" }

func TestStarlarkMarshal(t *testing.T) {
	type DummyEmbeddedStruct struct {
		A string
	}

	for i, scenario := range []starlarkMarshalTest{{
		from: starlark.MakeInt(-1),
		to:   starlark.MakeInt(-1),
	}, {
		from: nil,
		to:   starlark.None,
	}, {
		from: true,
		to:   starlark.True,
	}, {
		from: int(-1),
		to:   starlark.MakeInt(-1),
	}, {
		from: int8(-1),
		to:   starlark.MakeInt(-1),
	}, {
		from: int16(-1),
		to:   starlark.MakeInt(-1),
	}, {
		from: int32(-1),
		to:   starlark.MakeInt(-1),
	}, {
		from: int64(-1),
		to:   starlark.MakeInt(-1),
	}, {
		from: uint(1),
		to:   starlark.MakeInt(1),
	}, {
		from: uint8(1),
		to:   starlark.MakeInt(1),
	}, {
		from: uint16(1),
		to:   starlark.MakeInt(1),
	}, {
		from: uint32(1),
		to:   starlark.MakeInt(1),
	}, {
		from: uint64(1),
		to:   starlark.MakeInt(1),
	}, {
		from: float32(0.5),
		to:   starlark.Float(0.5),
	}, {
		from: float64(0.5),
		to:   starlark.Float(0.5),
	}, {
		from: func() any {
			v := 1

			return &v
		}(),
		to: starlark.MakeInt(1),
	}, {
		from: 'a',
		to:   starlark.MakeInt(97), // runes are indistinguishable from int32s.
	}, {
		from: "foo",
		to:   starlark.String("foo"),
	}, {
		from: []bool{true, false},
		to:   starlark.NewList([]starlark.Value{starlark.True, starlark.False}),
	}, {
		from: [...]bool{true, false},
		to:   starlark.NewList([]starlark.Value{starlark.True, starlark.False}),
	}, {
		from: []struct{ A, B string }{{A: "a1", B: "b1"}, {A: "a2", B: "b2"}},
		to: func() starlark.Value {
			d1 := starlark.NewDict(2)
			assert.NoError(t, d1.SetKey(starlark.String("A"), starlark.String("a1")))
			assert.NoError(t, d1.SetKey(starlark.String("B"), starlark.String("b1")))
			s1 := &starlarkObject{d: d1}

			d2 := starlark.NewDict(2)
			assert.NoError(t, d2.SetKey(starlark.String("A"), starlark.String("a2")))
			assert.NoError(t, d2.SetKey(starlark.String("B"), starlark.String("b2")))
			s2 := &starlarkObject{d: d2}

			return starlark.NewList([]starlark.Value{s1, s2})
		}(),
	}, {
		from: map[string]string{"a": "b", "c": "d"},
		to: func() starlark.Value {
			ret := starlark.NewDict(1)
			assert.NoError(t, ret.SetKey(starlark.String("a"), starlark.String("b")))
			assert.NoError(t, ret.SetKey(starlark.String("c"), starlark.String("d")))

			return ret
		}(),
	}, {
		from:      map[int]string{1: "1", 2: "2"},
		errPrefix: "Only string keys are supported, found int",
	}, {
		from:      map[*int]string{nil: "a"},
		errPrefix: "Only string keys are supported, found ptr",
	}, {
		from: struct {
			A string `json:"foo"`
			B string `json:"bar"`
		}{A: "a", B: "b"},
		to: func() starlark.Value {
			d1 := starlark.NewDict(2)
			assert.NoError(t, d1.SetKey(starlark.String("foo"), starlark.String("a")))
			assert.NoError(t, d1.SetKey(starlark.String("bar"), starlark.String("b")))
			ret := &starlarkObject{d: d1}

			return ret
		}(),
	}, {
		from: struct{ DummyEmbeddedStruct }{DummyEmbeddedStruct: DummyEmbeddedStruct{A: "a"}},
		to: func() starlark.Value {
			d1 := starlark.NewDict(1)
			assert.NoError(t, d1.SetKey(starlark.String("A"), starlark.String("a")))
			ret := &starlarkObject{d: d1}

			return ret
		}(),
	}, {
		from: struct{ fmt.Stringer }{Stringer: DummyStringer(0xbaa)},
		to: func() starlark.Value {
			d1 := starlark.NewDict(1)
			assert.NoError(t, d1.SetKey(starlark.String("Stringer"), starlark.MakeInt(0xbaa)))
			ret := &starlarkObject{d: d1}

			return ret
		}(),
	}, {
		from: struct {
			fmt.Stringer `json:"foo"`
		}{Stringer: DummyStringer(0xbaa)},
		to: func() starlark.Value {
			d1 := starlark.NewDict(1)
			assert.NoError(t, d1.SetKey(starlark.String("foo"), starlark.MakeInt(0xbaa)))
			ret := &starlarkObject{d: d1}

			return ret
		}(),
	}, {
		from:      func() {},
		errPrefix: "Unrecognised type func()",
	}, {
		from:      make(chan int),
		errPrefix: "Unrecognised type chan int",
	}, {
		from: TopStruct{
			MiddleStruct: MiddleStruct{
				LowerStruct: LowerStruct{
					Config: map[string]string{"name": "foo"},
				},
			},
		},
		to: func() starlark.Value {
			config := starlark.NewDict(1)
			assert.NoError(t, config.SetKey(starlark.String("name"), starlark.String("foo")))

			d1 := starlark.NewDict(1)
			assert.NoError(t, d1.SetKey(starlark.String("config"), config))
			ret := &starlarkObject{d: d1, typeName: "TopStruct"}

			return ret
		}(),
	}} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			sv, err := StarlarkMarshal(scenario.from)
			if scenario.errPrefix == "" {
				assert.NoError(t, err)
			} else {
				assert.True(t, strings.HasPrefix(err.Error(), scenario.errPrefix))
			}

			assert.Equal(t, scenario.to, sv)
		})
	}
}
