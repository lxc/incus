package filter

// OperatorSet is represents the types of operators and symbols that a filter can support.
type OperatorSet struct {
	And       string
	Or        string
	Equals    string
	NotEquals string

	GreaterThan  string
	LessThan     string
	GreaterEqual string
	LessEqual    string

	Negate string
	Quote  []string
}

// isValid ensures the OperatorSet has valid fields for the minimum supported operators.
func (o *OperatorSet) isValid() bool {
	return o.And != "" && o.Or != "" && o.Equals != "" && o.NotEquals != "" && o.Negate != "" && len(o.Quote) > 0
}

// QueryOperatorSet returns the default operator set for REST API queries.
func QueryOperatorSet() OperatorSet {
	return OperatorSet{
		And:       "and",
		Or:        "or",
		Equals:    "eq",
		NotEquals: "ne",
		Negate:    "not",
		Quote:     []string{"\""},
	}
}
