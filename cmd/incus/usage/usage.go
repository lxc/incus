package usage

import (
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/internal/i18n"
)

// makeList is a helper function building list atoms.
func makeList(atom Atom, minOccurrences int, separator ...string) Atom {
	if len(separator) == 0 {
		return list{atom, minOccurrences, " "}
	}

	return list{atom, minOccurrences, separator[0]}
}

// makeList is a helper function building optional atoms.
func makeOptional(atom Atom, chain []Atom) Atom {
	if len(chain) == 0 {
		return optional{atom}
	}

	return optional{compound{" ", append([]Atom{atom}, chain...)}}
}

// Atom is the type of command-line atoms.
type Atom interface {
	List(minOccurrences int, separator ...string) Atom
	Optional(chain ...Atom) Atom
	Render() string
	Remote() Atom
}

// alternative represents alternatives between several atoms.
type alternative struct {
	atoms []Atom
}

// List makes the atom accept a list.
func (a alternative) List(minOccurrences int, separator ...string) Atom {
	return makeList(a, minOccurrences, separator...)
}

// Optional makes the atom optional.
func (a alternative) Optional(chain ...Atom) Atom {
	return makeOptional(a, chain)
}

// Render renders the atom's usage string.
func (a alternative) Render() string {
	elements := make([]string, len(a.atoms))
	for i, atom := range a.atoms {
		elements[i] = atom.Render()
	}

	return "(" + strings.Join(elements, "|") + ")"
}

// Remote prefixes the atom with a remote.
func (a alternative) Remote() Atom {
	return remote{a}
}

// compound represents a sequence of atoms separated with a separator.
type compound struct {
	separator string
	atoms     []Atom
}

// List makes the atom accept a list.
func (c compound) List(minOccurrences int, separator ...string) Atom {
	return makeList(c, minOccurrences, separator...)
}

// Optional makes the atom optional.
func (c compound) Optional(chain ...Atom) Atom {
	return makeOptional(c, chain)
}

// Render renders the atom's usage string.
func (c compound) Render() string {
	if len(c.atoms) == 1 {
		return c.atoms[0].Render()
	}

	var sb strings.Builder
	firstNonOptionalAtom := 0
	for i, atom := range c.atoms {
		// If our atom is optional, its separator should be included in the optional string, because it
		// wouldn't appear if the atom is not specified by the user.
		o, ok := atom.(optional)
		// Spaces convey no semantic value in this case, so it feels more natural not to include them in
		// optional blocks (`<foo> [<bar>]` vs `<foo>[ <bar>]`).
		if ok && c.separator != " " {
			if i == firstNonOptionalAtom {
				// If optional atoms appear at the beginning of the compound atom, they behave differently
				// with regard to the separator (`[<foo>/][<bar>/]<baz>` vs `[<foo>][/<bar>]/<baz>`).
				sb.WriteString(optional{verbatim{o.atom.Render() + c.separator}}.Render())
				firstNonOptionalAtom++
			} else {
				sb.WriteString(optional{verbatim{c.separator + o.atom.Render()}}.Render())
			}
		} else {
			if i == firstNonOptionalAtom {
				// The separator must be omitted at the beginning of the compound, or just after optional
				// atoms at the beginning of the compound.
				sb.WriteString(atom.Render())
			} else {
				sb.WriteString(c.separator + atom.Render())
			}
		}
	}

	return sb.String()
}

// Remote prefixes the atom with a remote.
func (c compound) Remote() Atom {
	return remote{c}
}

// list represents a list of atoms of arbitrary length.
type list struct {
	atom           Atom
	minOccurrences int
	separator      string
}

// List makes the atom accept a list.
func (l list) List(minOccurrences int, separator ...string) Atom {
	return makeList(l, minOccurrences, separator...)
}

// Optional makes the atom optional.
func (l list) Optional(chain ...Atom) Atom {
	return makeOptional(list{l.atom, max(l.minOccurrences, 1), l.separator}, chain)
}

// Render renders the atom's usage string.
func (l list) Render() string {
	switch l.minOccurrences {
	case 0:
		// Lists with 0 minimum elements behave like optional lists with at least 1 element.
		return optional{list{l.atom, 1, l.separator}}.Render()
	case 1:
		element := l.atom.Render()
		if l.separator == " " {
			// If the separator is a space, `...` is widely understood as a valid repetition token
			// (`<foo>...`).
			return fmt.Sprintf(i18n.G("%s..."), element)
		}

		// Else, we are a bit more explicit (e.g., with `,` as the separator, `<foo>[,<foo>...]`).
		return element + optional{verbatim{fmt.Sprintf(i18n.G("%s..."), l.separator+element)}}.Render()
	default:
		// We recurse when the list has more that 1 minimum elements.
		return l.atom.Render() + l.separator + list{l.atom, l.minOccurrences - 1, l.separator}.Render()
	}
}

// Remote prefixes the atom with a remote.
func (l list) Remote() Atom {
	// It doesn't really make sense to prefix a list with a remote, so we distribute the operation.
	return remote{l.atom}.List(l.minOccurrences, l.separator)
}

// optional represents an optional atom.
type optional struct {
	atom Atom
}

// List makes the atom accept a list.
func (o optional) List(minOccurrences int, separator ...string) Atom {
	// We define optional.List as list.Optional.
	return makeList(o.atom, minOccurrences, separator...).Optional()
}

// Optional makes the atom optional.
func (o optional) Optional(chain ...Atom) Atom {
	return makeOptional(o.atom, chain)
}

// Render renders the atom's usage string.
func (o optional) Render() string {
	return "[" + o.atom.Render() + "]"
}

// Remote prefixes the atom with a remote.
func (o optional) Remote() Atom {
	return remote{o}
}

// placeholder represents a placeholder atom.
type placeholder struct {
	element string
}

// List makes the atom accept a list.
func (p placeholder) List(minOccurrences int, separator ...string) Atom {
	return makeList(p, minOccurrences, separator...)
}

// Optional makes the atom optional.
func (p placeholder) Optional(chain ...Atom) Atom {
	return makeOptional(p, chain)
}

// Render renders the atom's usage string.
func (p placeholder) Render() string {
	return "<" + p.element + ">"
}

// Remote prefixes the atom with a remote.
func (p placeholder) Remote() Atom {
	return remote{p}
}

// remote represents an atom prefixed with a remote.
type remote struct {
	suffix Atom
}

// List makes the atom accept a list.
func (r remote) List(minOccurrences int, separator ...string) Atom {
	return makeList(r, minOccurrences, separator...)
}

// Optional makes the atom optional.
func (r remote) Optional(chain ...Atom) Atom {
	return makeOptional(r, chain)
}

// Render renders the atom's usage string.
func (r remote) Render() string {
	return RemoteColonOpt.Render() + r.suffix.Render()
}

// Remote prefixes the atom with a remote.
func (r remote) Remote() Atom {
	// This is obviously a no-op.
	return r
}

// remote represents a verbatim atom.
type verbatim struct {
	element string
}

// List makes the atom accept a list.
func (v verbatim) List(minOccurrences int, separator ...string) Atom {
	return makeList(v, minOccurrences, separator...)
}

// Optional makes the atom optional.
func (v verbatim) Optional(chain ...Atom) Atom {
	return makeOptional(v, chain)
}

// Render renders the atom's usage string.
func (v verbatim) Render() string {
	return v.element
}

// Remote prefixes the atom with a remote.
func (v verbatim) Remote() Atom {
	return remote{v}
}

// A few strings used throughout the Incus client.
var (
	ACL                = placeholder{i18n.G("ACL")}
	Address            = placeholder{i18n.G("address")}
	AddressSet         = placeholder{i18n.G("address set")}
	Alias              = placeholder{i18n.G("alias")}
	Backend            = placeholder{i18n.G("backend")}
	BackupFile         = placeholder{i18n.G("backup file")}
	Bucket             = placeholder{i18n.G("bucket")}
	Client             = placeholder{i18n.G("client")}
	Device             = placeholder{i18n.G("device")}
	Direction          = placeholder{i18n.G("direction")}
	Directory          = placeholder{i18n.G("directory")}
	Driver             = placeholder{i18n.G("driver")}
	Expiry             = placeholder{i18n.G("expiry")}
	File               = placeholder{i18n.G("file")}
	Filter             = placeholder{i18n.G("filter")}
	Fingerprint        = placeholder{i18n.G("fingerprint")}
	Group              = placeholder{i18n.G("group")}
	Image              = placeholder{i18n.G("image")}
	Instance           = placeholder{i18n.G("instance")}
	Interface          = placeholder{i18n.G("interface")}
	ListenAddress      = placeholder{i18n.G("listen address")}
	ListenPort         = placeholder{i18n.G("listen port")}
	Key                = placeholder{i18n.G("key")}
	KV                 = compound{"=", []Atom{Key, Value}}
	Member             = placeholder{i18n.G("member")}
	Network            = placeholder{i18n.G("network")}
	NetworkIntegration = placeholder{i18n.G("network integration")}
	Operation          = placeholder{i18n.G("operation")}
	Path               = placeholder{i18n.G("path")}
	Peer               = placeholder{i18n.G("peer")}
	Pool               = placeholder{i18n.G("pool")}
	Port               = placeholder{i18n.G("port")}
	Profile            = placeholder{i18n.G("profile")}
	Project            = placeholder{i18n.G("project")}
	Protocol           = placeholder{i18n.G("protocol")}
	Query              = placeholder{i18n.G("query")}
	Record             = placeholder{i18n.G("record")}
	Remote             = placeholder{i18n.G("remote")}
	RemoteColonOpt     = Colon(Remote).Optional()
	Role               = placeholder{i18n.G("role")}
	Snapshot           = placeholder{i18n.G("snapshot")}
	SymlinkTargetPath  = placeholder{i18n.G("symlink target path")}
	Tarball            = placeholder{i18n.G("tarball")}
	Template           = placeholder{i18n.G("template")}
	Token              = placeholder{i18n.G("token")}
	Type               = placeholder{i18n.G("type")}
	URL                = placeholder{i18n.G("URL")}
	Value              = placeholder{i18n.G("value")}
	Volume             = placeholder{i18n.G("volume")}
	WarningUUID        = placeholder{i18n.G("warning UUID")}
	Zone               = placeholder{i18n.G("zone")}
)

// Either builds an alternative atom from several atoms.
func Either(atoms ...Atom) Atom {
	return alternative{atoms}
}

// EitherVerbatim builds an alternative of verbatim atoms from several strings.
func EitherVerbatim(elements ...string) Atom {
	atoms := make([]Atom, len(elements))
	for i, element := range elements {
		atoms[i] = verbatim{element}
	}

	return alternative{atoms}
}

// EitherPlaceholder builds an alternative of placeholder atoms from several strings.
func EitherPlaceholder(elements ...string) Atom {
	atoms := make([]Atom, len(elements))
	for i, element := range elements {
		atoms[i] = placeholder{element}
	}

	return alternative{atoms}
}

// NewName transforms a placeholder (e.g. `<foo>`) into a placeholder suggesting that a new name is
// requested (e.g. `<new foo name>`).
func NewName(p placeholder) Atom {
	return placeholder{fmt.Sprintf(i18n.G("new %s name"), p.element)}
}

// Target transforms a placeholder (e.g. `<foo>`) into a placeholder suggesting that the requested
// object is the target of an operation (e.g. `<target foo>`).
func Target(p placeholder) Atom {
	return placeholder{fmt.Sprintf(i18n.G("target %s"), p.element)}
}

// Colon suffixes an atom with `:`.
func Colon(a Atom) Atom {
	return compound{":", []Atom{a, verbatim{""}}}
}

// Dash builds an optional flag from a string.
func Dash(flag string) Atom {
	return verbatim{"--" + flag}.Optional()
}

// MakePath builds an atom compound separated by `/`.
func MakePath(atoms ...Atom) Atom {
	return compound{"/", atoms}
}

// Placeholder builds a placeholder atom from a string.
func Placeholder(element string) placeholder {
	return placeholder{element}
}

// Flags is an atom to be deleted in the future indicating to cobra that command-line flags should
// be where this atom is put. It will be replaced with something more correct semantically.
var Flags = verbatim{"flags"}.Optional()
