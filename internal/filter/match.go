package filter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// Match returns true if the given object matches the given filter.
func Match(obj any, set ClauseSet) (bool, error) {
	if set.ParseInt == nil {
		set.ParseInt = DefaultParseInt
	}

	if set.ParseUint == nil {
		set.ParseUint = DefaultParseUint
	}

	if set.ParseString == nil {
		set.ParseString = DefaultParseString
	}

	if set.ParseBool == nil {
		set.ParseBool = DefaultParseBool
	}

	if set.ParseRegexp == nil {
		set.ParseRegexp = DefaultParseRegexp
	}

	if set.ParseStringSlice == nil {
		set.ParseStringSlice = DefaultParseStringSlice
	}

	match := true

	for _, clause := range set.Clauses {
		value := ValueOf(obj, clause.Field)
		clauseMatch, err := set.match(clause, value)
		if err != nil {
			return false, err
		}

		// Finish out logic
		if clause.Not {
			clauseMatch = !clauseMatch
		}

		switch clause.PrevLogical {
		case set.Ops.And:
			match = match && clauseMatch
		case set.Ops.Or:
			match = match || clauseMatch
		default:
			return false, fmt.Errorf("unexpected clause operator")
		}
	}

	return match, nil
}

// DefaultParseInt converts the value of the clause to int64.
func DefaultParseInt(c Clause) (int64, error) {
	return strconv.ParseInt(c.Value, 10, 0)
}

// DefaultParseUint converts the value of the clause to Uint64.
func DefaultParseUint(c Clause) (uint64, error) {
	return strconv.ParseUint(c.Value, 10, 0)
}

// DefaultParseString converts the value of the clause to string.
func DefaultParseString(c Clause) (string, error) {
	return c.Value, nil
}

// DefaultParseBool converts the value of the clause to boolean.
func DefaultParseBool(c Clause) (bool, error) {
	return strconv.ParseBool(c.Value)
}

// DefaultParseRegexp converts the value of the clause to regexp.
func DefaultParseRegexp(c Clause) (*regexp.Regexp, error) {
	regexpValue := c.Value
	if !(strings.Contains(regexpValue, "^") || strings.Contains(regexpValue, "$")) {
		regexpValue = "^" + regexpValue + "$"
	}

	return regexp.Compile("(?i)" + regexpValue)
}

// DefaultParseStringSlice converts the value of the clause to a slice of string.
func DefaultParseStringSlice(c Clause) ([]string, error) {
	var val []string

	err := json.Unmarshal([]byte(c.Value), &val)
	if err != nil {
		return nil, err
	}

	return val, nil
}

func (s ClauseSet) match(c Clause, objValue any) (bool, error) {
	var valueStr string
	var valueRegexp *regexp.Regexp
	var valueInt int64
	var valueUint uint64
	var valueBool bool
	var valueSlice []string
	var err error

	// If 'value' is type of string try to test value as a regexp.
	valInfo := reflect.ValueOf(objValue)
	kind := valInfo.Kind()
	switch kind {
	case reflect.String:
		valueRegexp, _ = s.ParseRegexp(c)

		if valueRegexp == nil {
			valueStr, err = s.ParseString(c)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		valueInt, err = s.ParseInt(c)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		valueUint, err = s.ParseUint(c)
	case reflect.Bool:
		valueBool, err = s.ParseBool(c)
	case reflect.Slice:
		if reflect.TypeOf(objValue).Elem().Kind() == reflect.String {
			valueSlice, err = s.ParseStringSlice(c)
		} else {
			return false, fmt.Errorf("Invalid slice type %q for field %q", reflect.TypeOf(objValue).Elem().Kind(), c.Field)
		}

	default:
		return false, fmt.Errorf("Invalid type %q for field %q", kind.String(), c.Field)
	}

	if err != nil {
		return false, fmt.Errorf("Failed to parse value: %w", err)
	}

	switch c.Operator {
	case s.Ops.Equals:
		if valueRegexp != nil {
			return valueRegexp.MatchString(objValue.(string)), nil
		}

		switch val := objValue.(type) {
		case string:
			// Comparison is case insensitive.
			return strings.EqualFold(val, valueStr), nil
		case int, int8, int16, int32, int64:
			return objValue == valueInt, nil
		case uint, uint8, uint16, uint32, uint64:
			return objValue == valueUint, nil
		case bool:
			return objValue == valueBool, nil
		case []string:
			match := func() bool {
				if len(objValue.([]string)) != len(valueSlice) {
					return false
				}

				for k, v := range objValue.([]string) {
					if valueSlice[k] != v {
						return false
					}
				}

				return true
			}()

			return match, nil
		}

	case s.Ops.NotEquals:
		if valueRegexp != nil {
			return !valueRegexp.MatchString(objValue.(string)), nil
		}

		switch val := objValue.(type) {
		case string:
			// Comparison is case insensitive.
			return !strings.EqualFold(val, valueStr), nil
		case int, int8, int16, int32, int64:
			return objValue != valueInt, nil
		case uint, uint8, uint16, uint32, uint64:
			return objValue != valueUint, nil
		case bool:
			return objValue != valueBool, nil
		case []string:
			match := func() bool {
				if len(objValue.([]string)) != len(valueSlice) {
					return false
				}

				for k, v := range objValue.([]string) {
					if valueSlice[k] != v {
						return false
					}
				}

				return true
			}()

			return !match, nil
		}

	case s.Ops.GreaterThan:
		switch objValue.(type) {
		case string, bool, []string:
			return false, fmt.Errorf("Invalid operator %q for field %q", c.Operator, c.Field)
		case int, int8, int16, int32, int64:
			return valInfo.Int() > valueInt, nil
		case uint, uint8, uint16, uint32, uint64:
			return valInfo.Uint() > valueUint, nil
		}

	case s.Ops.LessThan:
		switch objValue.(type) {
		case string, bool, []string:
			return false, fmt.Errorf("Invalid operator %q for field %q", c.Operator, c.Field)
		case int, int8, int16, int32, int64:
			return valInfo.Int() < valueInt, nil
		case uint, uint8, uint16, uint32, uint64:
			return valInfo.Uint() < valueUint, nil
		}

	case s.Ops.GreaterEqual:
		switch objValue.(type) {
		case string, bool, []string:
			return false, fmt.Errorf("Invalid operator %q for field %q", c.Operator, c.Field)
		case int, int8, int16, int32, int64:
			return valInfo.Int() >= valueInt, nil
		case uint, uint8, uint16, uint32, uint64:
			return valInfo.Uint() >= valueUint, nil
		}

	case s.Ops.LessEqual:
		switch objValue.(type) {
		case string, bool, []string:
			return false, fmt.Errorf("Invalid operator %q for field %q", c.Operator, c.Field)
		case int, int8, int16, int32, int64:
			return valInfo.Int() <= valueInt, nil
		case uint, uint8, uint16, uint32, uint64:
			return valInfo.Uint() <= valueUint, nil
		}

	default:
		return false, fmt.Errorf("Unsupported operation")
	}

	return false, fmt.Errorf("Unsupported filter type %q for field %q", kind.String(), c.Field)
}
