package usage

// LegacyRemote displays `[<remote>:]ATOM` but also parses `[<remote>:] ATOM`.
func LegacyRemote(atom Atom) Atom {
	return hide{alternative{[]Atom{atom.Remote(), compound{" ", []Atom{RemoteColonOpt, atom}}}}, atom.Remote()}
}

// LegacyRemoteSynthesize transforms a parsed atom generated with `LegacyRemote` so that it behaves
// as if it was generated with `.Remote`.
func LegacyRemoteSynthesize(parsed *Parsed) {
	if parsed.BranchID > 0 {
		parsed.RemoteServer = parsed.List[0].RemoteServer
		parsed.RemoteName = parsed.List[0].RemoteName
		parsed.RemoteObject = parsed.List[1]
	}
}

// LegacyKV is a backward-compatible key/value parsing atom.
var LegacyKV = hide{alternative{[]Atom{compound{"=", []Atom{Key, Value}}, compound{" ", []Atom{Key, Value}}}}, compound{"=", []Atom{Key, Value}}}
