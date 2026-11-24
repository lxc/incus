// Command generate-pot writes the translation template from the i18n calls in the given Go files.
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// message describes one msgid found in the sources.
type message struct {
	locations []string
	plural    string
	format    bool
}

func main() {
	output := flag.String("o", "po/incus.pot", "Output file")
	packageName := flag.String("package-name", "incus", "Project-Id-Version header value")
	bugsAddress := flag.String("msgid-bugs-address", "lxc-devel@lists.linuxcontainers.org", "Report-Msgid-Bugs-To header value")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "Usage: generate-pot [flags] DIRECTORY...")
		os.Exit(1)
	}

	messages, err := extractMessages(flag.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(*output, []byte(renderTemplate(messages, *packageName, *bugsAddress)), 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// renderTemplate writes the template the way xgettext does, sorted by msgid with keywords padded to column 8.
func renderTemplate(messages map[string]*message, packageName string, bugsAddress string) string {
	// Sort on the escaped form, which is what xgettext --sort-output compares.
	msgids := make([]string, 0, len(messages))
	for msgid := range messages {
		msgids = append(msgids, msgid)
	}

	slices.SortFunc(msgids, func(a string, b string) int {
		return strings.Compare(escapePo(a), escapePo(b))
	})

	b := &strings.Builder{}
	b.WriteString(`# SOME DESCRIPTIVE TITLE.
# Copyright (C) YEAR THE PACKAGE'S COPYRIGHT HOLDER
# This file is distributed under the same license as the PACKAGE package.
# FIRST AUTHOR <EMAIL@ADDRESS>, YEAR.
#
#, fuzzy
msgid   ""
msgstr  "Project-Id-Version: ` + packageName + `\n"
        "Report-Msgid-Bugs-To: ` + bugsAddress + `\n"
        "POT-Creation-Date: ` + time.Now().Format("2006-01-02 15:04-0700") + `\n"
        "PO-Revision-Date: YEAR-MO-DA HO:MI+ZONE\n"
        "Last-Translator: FULL NAME <EMAIL@ADDRESS>\n"
        "Language-Team: LANGUAGE <LL@li.org>\n"
        "Language: \n"
        "MIME-Version: 1.0\n"
        "Content-Type: text/plain; charset=UTF-8\n"
        "Content-Transfer-Encoding: 8bit\n"

`)

	for _, msgid := range msgids {
		msg := messages[msgid]

		fmt.Fprintf(b, "#: %s\n", strings.Join(msg.locations, " "))
		if msg.format {
			b.WriteString("#, c-format\n")
		}

		writeValue(b, "msgid", msgid)
		if msg.plural != "" {
			writeValue(b, "msgid_plural", msg.plural)
			b.WriteString("msgstr[0]  \"\"\n")
			b.WriteString("msgstr[1]  \"\"\n")
		} else {
			writeValue(b, "msgstr", "")
		}

		b.WriteString("\n")
	}

	return b.String()
}

// writeValue writes a keyword and its quoted value, splitting after every newline like xgettext.
func writeValue(b *strings.Builder, keyword string, value string) {
	segments := strings.SplitAfter(value, "\n")
	if segments[len(segments)-1] == "" && len(segments) > 1 {
		segments = segments[:len(segments)-1]
	}

	fmt.Fprintf(b, "%-8s\"%s\"\n", keyword, escapePo(segments[0]))
	for _, segment := range segments[1:] {
		fmt.Fprintf(b, "        \"%s\"\n", escapePo(segment))
	}
}

// escapePo escapes a string for a template value, tabs stay raw to match the existing template.
func escapePo(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)

	return strings.ReplaceAll(s, "\n", `\n`)
}
