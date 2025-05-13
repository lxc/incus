// Various utilities to generate/parse/manipulate SQL expressions.

package query

import (
	"fmt"
	"strings"
)

// Params returns a parameters expression with the given number of '?'
// placeholders. E.g. Params(2) -> "(?, ?)". Useful for IN and VALUES
// expressions.
func Params(n int) string {
	tokens := make([]string, n)
	for i := range n {
		tokens[i] = "?"
	}

	return fmt.Sprintf("(%s)", strings.Join(tokens, ", "))
}
