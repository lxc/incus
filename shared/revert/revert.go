package revert

// Hook is a function that can be added to the revert via the Add() function.
// These will be run in the reverse order that they were added if the reverter's Fail() function is called.
type Hook func()

// Reverter is a helper type to manage revert functions.
type Reverter struct {
	revertFuncs []Hook
}

// New returns a new Reverter.
func New() *Reverter {
	return &Reverter{}
}

// Add adds a revert function to the list to be run when Revert() is called.
func (r *Reverter) Add(f Hook) {
	r.revertFuncs = append(r.revertFuncs, f)
}

// Fail runs any revert functions in the reverse order they were added.
// Should be used with defer or when a task has encountered an error and needs to be reverted.
func (r *Reverter) Fail() {
	funcCount := len(r.revertFuncs)
	for k := range r.revertFuncs {
		// Run the revert functions in reverse order.
		k = funcCount - 1 - k
		r.revertFuncs[k]()
	}
}

// Success clears the revert functions previously added.
// Should be called on successful completion of a task to prevent revert functions from being run.
func (r *Reverter) Success() {
	r.revertFuncs = nil
}

// Clone returns a copy of the reverter with the current set of revert functions added.
// This can be used if you want to return a reverting function to an external caller but do not want to actually
// execute the previously deferred reverter.Fail() function.
func (r *Reverter) Clone() *Reverter {
	rNew := New()
	rNew.revertFuncs = append(make([]Hook, 0, len(r.revertFuncs)), r.revertFuncs...)

	return rNew
}
