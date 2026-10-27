package lex

import (
	"fmt"
)

// VarDecl holds information about a variable declaration.
type VarDecl struct {
	Name string
	Expr string
}

func (d VarDecl) String() string {
	return fmt.Sprintf("%s %s", d.Name, d.Expr)
}

// MethodSignature holds information about a method signature.
type MethodSignature struct {
	Comment  string    // Method comment
	Name     string    // Method name
	Receiver VarDecl   // Receiver name and type
	Args     []VarDecl // Method arguments
	Return   []string  // Return type
}

// Slice returns the type name of a slice of items of the given type.
func Slice(typ string) string {
	return fmt.Sprintf("[]%s", typ)
}

// Element is the reverse of Slice, returning the element type name the slice
// with given type.
func Element(typ string) string {
	return typ[len("[]"):]
}

// Star adds a "*" prefix to the given string.
func Star(s string) string {
	return "*" + s
}
