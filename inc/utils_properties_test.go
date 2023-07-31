package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type utilsPropertiesTestSuite struct {
	suite.Suite
}

func TestUtilsPropertiesTestSuite(t *testing.T) {
	suite.Run(t, new(utilsPropertiesTestSuite))
}

func (s *utilsPropertiesTestSuite) TestStringToTimeHookFuncValidData() {
	layout := time.RFC3339
	hook := stringToTimeHookFunc(layout)

	result, err := hook(reflect.TypeOf(""), reflect.TypeOf(time.Time{}), "2023-07-12T07:34:00Z")
	s.NoError(err)
	s.Equal(time.Date(2023, 7, 12, 7, 34, 0, 0, time.UTC), result)
}

func (s *utilsPropertiesTestSuite) TestStringToTimeHookFuncInvalidData() {
	layout := time.RFC3339
	hook := stringToTimeHookFunc(layout)

	_, err := hook(reflect.TypeOf(""), reflect.TypeOf(time.Time{}), "not a time")
	s.Error(err, "Expected an error but got nil")
}

func (s *utilsPropertiesTestSuite) TestStringToBoolHookFuncValidData() {
	hookFunc := stringToBoolHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, interface{}) (interface{}, error))

	result, err := hook(reflect.String, reflect.Bool, "t")
	s.NoError(err)
	s.Equal(true, result)
}

func (s *utilsPropertiesTestSuite) TestStringToBoolHookFuncInvalidData() {
	hookFunc := stringToBoolHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error))

	_, err := hook(reflect.String, reflect.Bool, "not a boolean")
	s.Error(err, "Expected an error but got nil")
}

func (s *utilsPropertiesTestSuite) TestStringToIntHookFuncValidData() {
	hookFunc := stringToIntHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error))

	result, err := hook(reflect.String, reflect.Int, "123")
	s.NoError(err)
	s.Equal(123, result)
}

func (s *utilsPropertiesTestSuite) TestStringToIntHookFuncInvalidData() {
	hookFunc := stringToIntHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error))

	_, err := hook(reflect.String, reflect.Int, "not an int")
	s.Error(err, "Expected an error but got nil")
}

func (s *utilsPropertiesTestSuite) TestStringToFloatHookFuncValidData() {
	hookFunc := stringToFloatHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error))

	result, err := hook(reflect.String, reflect.Float64, "123.45")
	s.NoError(err)
	s.Equal(123.45, result)
}

func (s *utilsPropertiesTestSuite) TestStringToFloatHookFuncInvalidData() {
	hookFunc := stringToFloatHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error))

	_, err := hook(reflect.String, reflect.Float64, "not a float")
	s.Error(err, "Expected an error but got nil")
}

type testStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (s *utilsPropertiesTestSuite) TestSetFieldByJsonTagSettable() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	setFieldByJsonTag(&ts, "name", "Jane Doe")
	s.Equal("Jane Doe", ts.Name)
}

func (s *utilsPropertiesTestSuite) TestSetFieldByJsonTagNonSettable() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	setFieldByJsonTag(&ts, "invalid name", "Jane Doe")
	s.NotEqual(ts.Name, "Jane Doe")
}

func (s *utilsPropertiesTestSuite) TestUnsetFieldByJsonTagValid() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	err := unsetFieldByJsonTag(&ts, "name")
	s.NoError(err)
	s.Equal("", ts.Name)
}

func (s *utilsPropertiesTestSuite) TestUnsetFieldByJsonTagInvalid() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	err := unsetFieldByJsonTag(&ts, "invalid")
	s.Error(err, "Expected an error but got nil")
}

type writableStruct struct {
	Name  string    `json:"name"`
	Age   int       `json:"age"`
	Score float64   `json:"score"`
	Alive bool      `json:"alive"`
	Birth time.Time `json:"birth"`
}

func (s *utilsPropertiesTestSuite) TestUnpackKVToWritable() {
	ws := &writableStruct{}
	keys := map[string]string{
		"name":  "John Doe",
		"age":   "30",
		"score": "85.5",
		"alive": "true",
		"birth": "2000-01-01T00:00:00Z",
	}

	err := unpackKVToWritable(ws, keys)
	s.NoError(err)

	s.Equal("John Doe", ws.Name)
	s.Equal(30, ws.Age)
	s.Equal(85.5, ws.Score)
	s.Equal(true, ws.Alive)
	s.Equal("2000-01-01T00:00:00Z", ws.Birth.Format(time.RFC3339))
}

func (s *utilsPropertiesTestSuite) TestUnpackKVToWritableInvalidData() {
	ws := &writableStruct{}
	keys := map[string]string{
		"name":  "John Doe",
		"age":   "not an int",
		"score": "not a float",
		"alive": "not a bool",
		"birth": "not a time",
	}

	err := unpackKVToWritable(ws, keys)
	s.Error(err, "Expected an error but got nil")
}
