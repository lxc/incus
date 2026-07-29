package cmd

import (
	"strings"

	"github.com/spf13/pflag"
)

// AddStringFlag adds a string flag to the given flag set.
func AddStringFlag(flags *pflag.FlagSet, flag *string, name string, defVal string, noOptDefVal string, usage string) {
	name, shorthand, _ := strings.Cut(name, "|")
	// Cobra handles value hints and backticks in a way that doesn’t suit us. Prepending two
	// backticks is a way to solve that.
	flags.StringVarP(flag, name, shorthand, defVal, "``"+usage)
	flags.Lookup(name).NoOptDefVal = noOptDefVal
}

// AddStringArrayFlag adds a string array flag to the given flag set.
func AddStringArrayFlag(flags *pflag.FlagSet, flag *[]string, name string, usage string) {
	name, shorthand, _ := strings.Cut(name, "|")
	// Cobra handles value hints and backticks in a way that doesn’t suit us. Prepending two
	// backticks is a way to solve that.
	flags.StringArrayVarP(flag, name, shorthand, nil, "``"+usage)
}

// AddIntFlag adds an integer flag to the given flag set.
func AddIntFlag(flags *pflag.FlagSet, flag *int, name string, usage string, defVal ...int) {
	name, shorthand, _ := strings.Cut(name, "|")
	var def int
	if len(defVal) > 0 {
		def = defVal[0]
	}

	// Cobra handles value hints and backticks in a way that doesn’t suit us. Prepending two
	// backticks is a way to solve that.
	flags.IntVarP(flag, name, shorthand, def, "``"+usage)
}

// AddBoolFlag adds a boolean flag to the given flag set.
func AddBoolFlag(flags *pflag.FlagSet, flag *bool, name string, usage string) {
	name, shorthand, _ := strings.Cut(name, "|")
	// Cobra handles value hints and backticks in a way that doesn’t suit us. Prepending two
	// backticks is a way to solve that.
	flags.BoolVarP(flag, name, shorthand, false, "``"+usage)
}

// AddUint32Flag adds a `uint32` flag to the given flag set.
func AddUint32Flag(flags *pflag.FlagSet, flag *uint32, name string, usage string, defVal ...uint32) {
	name, shorthand, _ := strings.Cut(name, "|")
	var def uint32
	if len(defVal) > 0 {
		def = defVal[0]
	}

	// Cobra handles value hints and backticks in a way that doesn’t suit us. Prepending two
	// backticks is a way to solve that.
	flags.Uint32VarP(flag, name, shorthand, def, "``"+usage)
}

// AddUint64Flag adds a `uint64` flag to the given flag set.
func AddUint64Flag(flags *pflag.FlagSet, flag *uint64, name string, usage string) {
	name, shorthand, _ := strings.Cut(name, "|")
	// Cobra handles value hints and backticks in a way that doesn’t suit us. Prepending two
	// backticks is a way to solve that.
	flags.Uint64VarP(flag, name, shorthand, 0, "``"+usage)
}

// AddInt64Flag adds an `int64` flag to the given flag set.
func AddInt64Flag(flags *pflag.FlagSet, flag *int64, name string, usage string) {
	name, shorthand, _ := strings.Cut(name, "|")
	// Cobra handles value hints and backticks in a way that doesn’t suit us. Prepending two
	// backticks is a way to solve that.
	flags.Int64VarP(flag, name, shorthand, 0, "``"+usage)
}
