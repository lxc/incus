package file

// Snippet generates a single code snippet of a target source file code.
type Snippet interface {
	Generate(buffer *Buffer) error
	GenerateSignature(buffer *Buffer) error
}
