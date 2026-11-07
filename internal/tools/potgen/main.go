// Command potgen writes the translation template po/incus.pot from the
// i18n.G and i18n.NG calls in the source tree.
//
// It exists because no available extractor is both correct and dependency
// free. github.com/snapcore/snapd/i18n/xgettext-go produces the right output
// but drags a GPL-3 module into the build. GNU xgettext 1.0 accepts the
// keyword but misses msgids nested inside composite literals, reports line
// numbers off by one, and tags format strings "go-format" instead of
// "c-format". This program is the port of xgettext-go's behaviour to plain
// Go with no dependencies outside the standard library.
//
// Usage:
//
//	go run ./internal/tools/potgen \
//	    -o po/incus.pot \
//	    --package-name=incus \
//	    --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org \
//	    --source 'cmd/incus/*.go' --source 'cmd/incus/color/*.go' \
//	    --source 'cmd/incus/usage/*.go' --source 'shared/cliconfig/*.go'
//
// With no --source the whole module is scanned.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// msgInfo accumulates what the template needs to say about one msgid.
type msgInfo struct {
	Locations []string // "file:line", in discovery order
	Flags     []string // gettext flags, currently only "c-format"
	Plural    string   // msgid_plural, empty for singular calls
}

// addLocation records a location, de-duplicated but otherwise in discovery
// order.
func (m *msgInfo) addLocation(loc string) {
	if loc == "" {
		return
	}

	for _, existing := range m.Locations {
		if existing == loc {
			return
		}
	}

	m.Locations = append(m.Locations, loc)
}

func main() {
	var (
		output       string
		sources      multiFlag
		packageName  string
		bugsAddress  string
		creationDate string
	)

	flag.StringVar(&output, "o", "po/incus.pot", "output file")
	flag.Var(&sources, "source", "DIR, FILE or GLOB to scan (repeatable; default: whole module)")
	flag.StringVar(&packageName, "package-name", "incus", "Project-Id-Version in the header")
	flag.StringVar(&bugsAddress, "msgid-bugs-address", "lxc-devel@lists.linuxcontainers.org", "Report-Msgid-Bugs-To in the header")
	flag.StringVar(&creationDate, "creation-date", "", "override POT-Creation-Date (default: now)")
	flag.Parse()

	if creationDate == "" {
		creationDate = time.Now().Format("2006-01-02 15:04-0700")
	}

	infos, err := scanGoFiles(".", sources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "potgen: %v\n", err)
		os.Exit(1)
	}

	if len(infos) == 0 {
		fmt.Fprintln(os.Stderr, "potgen: no i18n calls found")
		os.Exit(1)
	}

	data := writePot(infos, header(packageName, bugsAddress, creationDate))
	err = os.WriteFile(output, []byte(data), 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "potgen: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "potgen: %s (%d msgids)\n", output, len(infos))
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, " ") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// header renders the template preamble.
//
// Wording, field order and column alignment follow xgettext, so switching
// generators does not churn the header in review. The charset is written as
// UTF-8 directly, which makes the Makefile's trailing sed unnecessary.
func header(pkg, bugs, date string) string {
	return "# SOME DESCRIPTIVE TITLE.\n" +
		"# Copyright (C) YEAR THE PACKAGE'S COPYRIGHT HOLDER\n" +
		"# This file is distributed under the same license as the PACKAGE package.\n" +
		"# FIRST AUTHOR <EMAIL@ADDRESS>, YEAR.\n" +
		"#\n" +
		"#, fuzzy\n" +
		"msgid   \"\"\n" +
		"msgstr  \"Project-Id-Version: " + pkg + "\\n\"\n" +
		"        \"Report-Msgid-Bugs-To: " + bugs + "\\n\"\n" +
		"        \"POT-Creation-Date: " + date + "\\n\"\n" +
		"        \"PO-Revision-Date: YEAR-MO-DA HO:MI+ZONE\\n\"\n" +
		"        \"Last-Translator: FULL NAME <EMAIL@ADDRESS>\\n\"\n" +
		"        \"Language-Team: LANGUAGE <LL@li.org>\\n\"\n" +
		"        \"Language: \\n\"\n" +
		"        \"MIME-Version: 1.0\\n\"\n" +
		"        \"Content-Type: text/plain; charset=UTF-8\\n\"\n" +
		"        \"Content-Transfer-Encoding: 8bit\\n\"" +
		"\n"
}

// needsCFormat reports whether msgid looks like a printf-style format string.
//
// "%%" is a literal percent sign and must not count.
func needsCFormat(msgid string) bool {
	for i := 0; i < len(msgid); i++ {
		if msgid[i] != '%' {
			continue
		}

		if i+1 < len(msgid) && msgid[i+1] == '%' {
			i++
			continue
		}

		return true
	}

	return false
}

// potSegments splits value after each newline, keeping the newline at the end
// of its segment: "a\nb" yields ["a\n", "b"].
func potSegments(value string) []string {
	if !strings.Contains(value, "\n") {
		return []string{value}
	}

	var segs []string

	for len(value) > 0 {
		i := strings.IndexByte(value, '\n')
		if i < 0 {
			segs = append(segs, value)
			break
		}

		segs = append(segs, value[:i+1])
		value = value[i+1:]
	}

	return segs
}

// escapePO escapes a string for the template.
//
// Tabs are deliberately left as raw bytes: the checked-in incus.pot has raw
// tabs, msgmerge treats a raw tab and a "\t" escape as the same msgid, and
// keeping them raw is what makes the output byte-identical to xgettext-go's.
func escapePO(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)

	return strings.ReplaceAll(s, "\n", `\n`)
}

// quotePO wraps a string in template quotes with proper escaping.
func quotePO(s string) string {
	return `"` + escapePO(s) + `"`
}

// writeValue writes one keyword/value pair the way xgettext does: the keyword
// padded to column 8, continuation lines indented by 8 spaces.
func writeValue(b *strings.Builder, keyword, value string) {
	pad := 8 - len(keyword)
	if pad < 1 {
		pad = 1
	}

	segs := potSegments(value)

	b.WriteString(keyword)
	b.WriteString(strings.Repeat(" ", pad))
	b.WriteString(quotePO(segs[0]))
	b.WriteByte('\n')

	for _, seg := range segs[1:] {
		b.WriteString("        ")
		b.WriteString(quotePO(seg))
		b.WriteByte('\n')
	}
}

// writePot renders the template.
//
// Layout is xgettext's: msgids sorted, one "#:" line per entry, an optional
// "#," line, keywords padded to column 8, and a blank line after every block
// including the last.
func writePot(infos map[string]*msgInfo, head string) string {
	keys := make([]string, 0, len(infos))
	for msgid := range infos {
		keys = append(keys, msgid)
	}

	// Sort by the escaped form, i.e. by what appears between the quotes. A
	// newline sorts as backslash-n (0x5C), which comes after a space (0x20);
	// sorting the unescaped string would put 0x0A first and reorder entries.
	escaped := make(map[string]string, len(keys))
	for _, k := range keys {
		escaped[k] = escapePO(k)
	}

	sort.Slice(keys, func(i, j int) bool {
		return escaped[keys[i]] < escaped[keys[j]]
	})

	var b strings.Builder

	if head != "" {
		b.WriteString(head)
		if !strings.HasSuffix(head, "\n\n") {
			b.WriteByte('\n')
		}
	}

	for _, msgid := range keys {
		info := infos[msgid]

		if len(info.Locations) > 0 {
			b.WriteString("#:")
			for _, loc := range info.Locations {
				b.WriteByte(' ')
				b.WriteString(loc)
			}

			b.WriteByte('\n')
		}

		if len(info.Flags) > 0 {
			b.WriteString("#, ")
			b.WriteString(strings.Join(info.Flags, ", "))
			b.WriteByte('\n')
		}

		writeValue(&b, "msgid", msgid)
		if info.Plural != "" {
			writeValue(&b, "msgid_plural", info.Plural)
			b.WriteString("msgstr[0]  \"\"\n")
			b.WriteString("msgstr[1]  \"\"\n")
		} else {
			writeValue(&b, "msgstr", "")
		}

		b.WriteByte('\n')
	}

	return b.String()
}
