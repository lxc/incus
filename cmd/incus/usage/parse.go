package usage

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	cliColor "github.com/lxc/incus/v6/cmd/incus/color"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/cliconfig"
)

// ExplainOnly is a global switch putting the parser into explain mode, i.e. showing the user how
// their arguments are parsed.
var ExplainOnly = false

func quote(s string) string {
	return fmt.Sprintf(i18n.G("“%s”"), s)
}

func formatAlternatives(alternatives []string) string {
	n := len(alternatives)
	if n == 1 {
		return quote(alternatives[0])
	}

	quoted := make([]string, n)
	for i, exp := range alternatives {
		quoted[i] = quote(exp)
	}

	return fmt.Sprintf(i18n.G("one of %s or %s"), strings.Join(quoted[:n-1], i18n.G(", ")), quoted[n-1])
}

type notEnoughArgumentsError struct {
	atom Atom
}

func (a *notEnoughArgumentsError) Error() string {
	// This special case handles compounds that end with a suffix (and therefore end with a blank
	// verbatim atom.
	v, ok := a.atom.(verbatim)
	if ok && v.element == "" {
		return i18n.G("unexpected end of argument; did you forget a suffix?")
	}

	return fmt.Sprintf(i18n.G("not enough arguments; expected a value for %s"), quote(renderRaw(a.atom)))
}

type tooManyArgumentsError struct {
	args []string
}

func (t *tooManyArgumentsError) Error() string {
	escapedArgs := make([]string, len(t.args))
	for i, arg := range t.args {
		if strings.Contains(arg, " ") {
			escapedArgs[i] = strconv.Quote(arg)
		} else {
			escapedArgs[i] = arg
		}
	}

	return fmt.Sprintf(i18n.G("too many arguments; unexpected %s"), quote(strings.Join(escapedArgs, " ")))
}

type argumentNotFullyConsumedError struct {
	rest   string
	parent string
}

func (a *argumentNotFullyConsumedError) Error() string {
	if a.rest == a.parent {
		return fmt.Sprintf(i18n.G("cannot parse this argument; unexpected %s"), quote(a.rest))
	}

	return fmt.Sprintf(i18n.G("cannot parse this argument; unexpected %s in %s"), quote(a.rest), quote(a.parent))
}

type argumentMismatchError struct {
	arg      string
	expected []string
}

func (a *argumentMismatchError) Error() string {
	n := len(a.expected)
	if n == 0 {
		return fmt.Sprintf(i18n.G("unexpected %s"), quote(a.arg))
	}

	if a.arg == "" {
		return fmt.Sprintf(i18n.G("expected %s"), formatAlternatives(a.expected))
	}

	return fmt.Sprintf(i18n.G("unexpected %s; expected %s"), quote(a.arg), formatAlternatives(a.expected))
}

func isParsingError(err error) bool {
	switch err.(type) {
	case *notEnoughArgumentsError, *tooManyArgumentsError, *argumentNotFullyConsumedError, *argumentMismatchError:
		return true
	default:
		return false
	}
}

// ErrExplainOnly is returned when --explain is used and the CLI invocation is valid.
var ErrExplainOnly = errors.New(i18n.G("This command was called with --explain; its arguments are valid, but no further processing is done"))

// Parsed is the type of parsed atoms.
type Parsed struct {
	// The atom which has been parsed.
	source Atom
	// The remote name, if the parsed atom describes a remote.
	RemoteName string
	// The remote instance server, if the parsed atom describes a remote.
	RemoteServer incus.InstanceServer
	// The remote object parsed sub-atom, if the parsed atom describes a remote.
	RemoteObject *Parsed
	// The string argument(s) mapped to this parsed atom.
	String string
	// The parsed sub-atoms.
	List []*Parsed
	// The parsed sub-atoms as strings.
	StringList []string
	// The error that led to the atom parsing being skipped.
	err error
	// Whether the atom parsing has been skipped.
	Skipped bool
	// The branch number of an alternative.
	BranchID int
}

// Get gets a parsed atom’s string representation, or a default value if the atom was skipped.
func (p Parsed) Get(def string) string {
	if p.Skipped {
		return def
	}

	return p.String
}

func underline(width int, cursor *int) (string, int) {
	var str string
	switch width {
	case 1:
		str = "┬"
	case 2:
		str = "├┘"
	default:
		dashCount := width - 3
		str = "└" + strings.Repeat("─", dashCount/2) + "┬" + strings.Repeat("─", dashCount-(dashCount/2)) + "┘"
	}

	middle := *cursor + (width-1)/2
	*cursor = *cursor + width + 1
	return str, middle
}

func (u Usage) diagnose(cmd *cobra.Command, parsedValues []*Parsed, parseRTL bool) {
	nAtoms := len(u)
	renderedAtoms := make([]string, nAtoms)

	// To properly support international characters, we have to count printed columns and not bytes.
	wcWidths := make([]int, nAtoms)

	for i, atom := range u {
		renderedAtoms[i] = atom.Render()
		wcWidths[i] = runewidth.StringWidth(renderRaw(atom))
	}

	commandPath := cmd.CommandPath()
	fmt.Println(cliColor.UsagePrefix + " " + commandPath + " " + strings.Join(renderedAtoms, " "))
	parsedCount := len(parsedValues)
	usagePrefixLen := runewidth.StringWidth(cliColor.RawUsagePrefix) + runewidth.StringWidth(commandPath) + 1
	cursor := 0
	// We shrink renderedAtoms to the atoms we actually parsed.
	if parseRTL {
		slices.Reverse(parsedValues)
		renderedAtoms = renderedAtoms[nAtoms-parsedCount:]
		for i := range nAtoms - parsedCount {
			cursor = cursor + wcWidths[i] + 1
		}
	} else {
		renderedAtoms = renderedAtoms[:parsedCount]
	}

	padding := usagePrefixLen + 1
	if parsedCount > 0 {
		underlinedAtoms := make([]string, parsedCount)
		underlinedAtomMids := make([]int, parsedCount)
		offset := 0
		if parseRTL {
			offset = nAtoms - parsedCount
		}

		for i := range parsedCount {
			str, middle := underline(wcWidths[offset+i], &cursor)
			underlinedAtoms[i] = str
			underlinedAtomMids[i] = middle
		}

		if parseRTL {
			if parsedCount < nAtoms {
				underlinedAtoms = append([]string{color.RedString(strings.Repeat("┅", wcWidths[offset-1]))}, underlinedAtoms...)
			}

			// In RTL mode, we need to properly pad the strings so that the diagnosis is right-aligned.
			for i := range offset - 1 {
				padding = padding + wcWidths[i] + 1
			}
		} else {
			if parsedCount < nAtoms {
				underlinedAtoms = append(underlinedAtoms, color.RedString(strings.Repeat("┅", wcWidths[parsedCount])))
			}
		}

		fmt.Println(strings.Repeat(" ", padding) + strings.Join(underlinedAtoms, " "))

		for i := range parsedCount {
			fmt.Print(strings.Repeat(" ", parsedCount+1-i) + "┌" + strings.Repeat("│", i) + strings.Repeat("─", usagePrefixLen+underlinedAtomMids[i]-parsedCount-1) + "┘")

			j := i + 1
			for j < parsedCount {
				fmt.Print(strings.Repeat(" ", underlinedAtomMids[j]-underlinedAtomMids[j-1]-1), "│")
				j++
			}

			fmt.Print("\n")
		}

		for i, parsedValue := range parsedValues {
			fmt.Print("  ", strings.Repeat("│", parsedCount-i-1)+"└"+strings.Repeat("─", i+1)+" ")
			if parsedValue.err != nil {
				color.New(color.Faint).Printf(i18n.G("(skipped: %s)\n"), parsedValue.err)
			} else if parsedValue.Skipped {
				color.New(color.Faint).Println(i18n.G("(skipped: no value given)"))
			} else {
				fmt.Println(quote(parsedValue.String))
			}
		}
	} else if nAtoms > 0 {
		i := 0
		if parseRTL {
			// In RTL mode, we need to properly pad the strings so that the diagnosis is right-aligned.
			for i < nAtoms-1 {
				padding = padding + wcWidths[i] + 1
				i++
			}
		}

		fmt.Println(strings.Repeat(" ", padding) + color.RedString(strings.Repeat("┅", wcWidths[i])))
	}

	// This makes the output error/status a bit easier to read.
	fmt.Println()
}

// Parse parses a usage.
func (u Usage) Parse(conf *cliconfig.Config, cmd *cobra.Command, args []string, rtl ...bool) ([]*Parsed, error) {
	// Build a local server cache.
	servers := map[string]incus.InstanceServer{}
	nArgs := len(args)
	nAtoms := len(u)
	parseRTL := false
	if len(rtl) > 0 {
		parseRTL = rtl[0]
	}

	argsInUse := args
	atoms := u
	if parseRTL {
		slices.Reverse(argsInUse)

		// We don’t want to modify the original slice, as this may be reused.
		atoms = make([]Atom, nAtoms)
		for i, atom := range u {
			atoms[nAtoms-1-i] = atom
		}
	}

	var result []*Parsed
	for _, atom := range atoms {
		p, err := atom.Parse(conf, cmd, servers, &argsInUse, parseRTL)
		if err != nil {
			_, ok := err.(*notEnoughArgumentsError)
			if ok && nArgs == 0 {
				_ = cmd.Help()
				// This makes the output error a bit easier to read.
				fmt.Println()
				return nil, err
			}

			u.diagnose(cmd, result, parseRTL)
			return nil, err
		}

		result = append(result, p)
	}

	if len(argsInUse) != 0 {
		err := &tooManyArgumentsError{argsInUse}
		u.diagnose(cmd, result, parseRTL)
		return nil, err
	}

	if ExplainOnly {
		u.diagnose(cmd, result, parseRTL)
		return nil, ErrExplainOnly
	}

	if parseRTL {
		slices.Reverse(result)
	}

	return result, nil
}
