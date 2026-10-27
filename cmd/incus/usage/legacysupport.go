package usage

import (
	"github.com/lxc/incus/v7/internal/i18n"
)

// LegacyKV is a backward-compatible key/value parsing atom.
var LegacyKV = hide{alternative{[]Atom{compound{"=", []Atom{Key, Value}}, deprecated{compound{" ", []Atom{Key, Value}}, i18n.G("please switch to the “<key>=<value>” syntax")}}}, compound{"=", []Atom{Key, Value}}}
