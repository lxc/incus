package jmap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- GetString ---

func TestMap_GetString_Valid(t *testing.T) {
	// GIVEN
	m := Map{"name": "incus"}

	// WHEN
	got, err := m.GetString("name")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, "incus", got)
}

func TestMap_GetString_EmptyString(t *testing.T) {
	// GIVEN
	m := Map{"name": ""}

	// WHEN
	got, err := m.GetString("name")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestMap_GetString_MissingKey(t *testing.T) {
	// GIVEN
	m := Map{}

	// WHEN
	got, err := m.GetString("name")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, "", got)
	assert.Equal(t, "Response was missing `name`", err.Error())
}

func TestMap_GetString_WrongTypeInt(t *testing.T) {
	// GIVEN
	m := Map{"name": 42}

	// WHEN
	got, err := m.GetString("name")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, "", got)
	assert.Equal(t, "`name` was not a string", err.Error())
}

func TestMap_GetString_WrongTypeBool(t *testing.T) {
	// GIVEN
	m := Map{"name": true}

	// WHEN
	got, err := m.GetString("name")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, "", got)
	assert.Equal(t, "`name` was not a string", err.Error())
}

// --- GetMap ---

func TestMap_GetMap_Valid(t *testing.T) {
	// GIVEN
	m := Map{"inner": map[string]any{"name": "incus"}}

	// WHEN
	got, err := m.GetMap("inner")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, Map{"name": "incus"}, got)
}

func TestMap_GetMap_EmptyMap(t *testing.T) {
	// GIVEN
	m := Map{"inner": map[string]any{}}

	// WHEN
	got, err := m.GetMap("inner")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, Map{}, got)
}

func TestMap_GetMap_MissingKey(t *testing.T) {
	// GIVEN
	m := Map{}

	// WHEN
	got, err := m.GetMap("inner")

	// THEN
	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, "Response was missing `inner`", err.Error())
}

func TestMap_GetMap_WrongTypeString(t *testing.T) {
	// GIVEN
	m := Map{"inner": "incus"}

	// WHEN
	got, err := m.GetMap("inner")

	// THEN
	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, "`inner` was not a map, got string", err.Error())
}

func TestMap_GetMap_WrongTypeInt(t *testing.T) {
	// GIVEN
	m := Map{"inner": 123}

	// WHEN
	got, err := m.GetMap("inner")

	// THEN
	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, "`inner` was not a map, got int", err.Error())
}

// --- GetInt ---

func TestMap_GetInt_Valid(t *testing.T) {
	// GIVEN
	m := Map{"num": 42.0}

	// WHEN
	got, err := m.GetInt("num")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 42, got)
}

func TestMap_GetInt_Negative(t *testing.T) {
	// GIVEN
	m := Map{"num": -10.0}

	// WHEN
	got, err := m.GetInt("num")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, -10, got)
}

func TestMap_GetInt_Zero(t *testing.T) {
	// GIVEN
	m := Map{"num": 0.0}

	// WHEN
	got, err := m.GetInt("num")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 0, got)
}

func TestMap_GetInt_FloatTruncated(t *testing.T) {
	// GIVEN
	m := Map{"num": 42.7}

	// WHEN
	got, err := m.GetInt("num")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 42, got)
}

func TestMap_GetInt_MissingKey(t *testing.T) {
	// GIVEN
	m := Map{}

	// WHEN
	got, err := m.GetInt("num")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, -1, got)
	assert.Equal(t, "Response was missing `num`", err.Error())
}

func TestMap_GetInt_WrongTypeString(t *testing.T) {
	// GIVEN
	m := Map{"num": "123"}

	// WHEN
	got, err := m.GetInt("num")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, -1, got)
	assert.Equal(t, "`num` was not an int", err.Error())
}

func TestMap_GetInt_WrongTypeBool(t *testing.T) {
	// GIVEN
	m := Map{"num": true}

	// WHEN
	got, err := m.GetInt("num")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, -1, got)
	assert.Equal(t, "`num` was not an int", err.Error())
}

// --- GetBool ---

func TestMap_GetBool_True(t *testing.T) {
	// GIVEN
	m := Map{"flag": true}

	// WHEN
	got, err := m.GetBool("flag")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, true, got)
}

func TestMap_GetBool_False(t *testing.T) {
	// GIVEN
	m := Map{"flag": false}

	// WHEN
	got, err := m.GetBool("flag")

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, false, got)
}

func TestMap_GetBool_MissingKey(t *testing.T) {
	// GIVEN
	m := Map{}

	// WHEN
	got, err := m.GetBool("flag")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, false, got)
	assert.Equal(t, "Response was missing `flag`", err.Error())
}

func TestMap_GetBool_WrongTypeString(t *testing.T) {
	// GIVEN
	m := Map{"flag": "true"}

	// WHEN
	got, err := m.GetBool("flag")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, false, got)
	assert.Equal(t, "`flag` was not a bool", err.Error())
}

func TestMap_GetBool_WrongTypeInt(t *testing.T) {
	// GIVEN
	m := Map{"flag": 1}

	// WHEN
	got, err := m.GetBool("flag")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, false, got)
	assert.Equal(t, "`flag` was not a bool", err.Error())
}

func TestMap_GetBool_WrongTypeIntZero(t *testing.T) {
	// GIVEN
	m := Map{"flag": 0}

	// WHEN
	got, err := m.GetBool("flag")

	// THEN
	assert.Error(t, err)
	assert.Equal(t, false, got)
	assert.Equal(t, "`flag` was not a bool", err.Error())
}
