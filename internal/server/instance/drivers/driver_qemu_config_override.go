package drivers

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
)

const pattern = `\s*(?m:(?:\[([^\]]+)\](?:\[(\d+)\])?)|(?:([^=]+)[ \t]*=[ \t]*(?:"([^"]*)"|([^\n]*)))$)`

var parser = regexp.MustCompile(pattern)

// sectionContent represents the content of a section, without its name.
type sectionContent struct {
	comment string
	entries map[string]string
}

// section represents a section pointing to its content.
type section struct {
	name    string
	content *sectionContent
}

// qemuRawCfgOverride generates a new QEMU configuration from an original one and an override entry.
func qemuRawCfgOverride(conf []cfg.Section, expandedConfig map[string]string) []cfg.Section {
	confOverride, ok := expandedConfig["raw.qemu.conf"]
	if !ok {
		// If we don’t have an override, we can return the original configuration.
		return conf
	}

	// We define a data structure optimized for lookup and insertion…
	indexedSections := map[string]map[int]*sectionContent{}
	// … and another one keeping insertion order. This saves us a few cycles at the expense of a few
	// bytes of RAM.
	orderedSections := []section{}

	// We first iterate over the original config to populate both our data structures.
	for _, sec := range conf {
		indexedSection, ok := indexedSections[sec.Name]
		if !ok {
			indexedSection = map[int]*sectionContent{}
			indexedSections[sec.Name] = indexedSection
		}

		// We perform a copy of the map to avoid modifying the original one
		entries := make(map[string]string, len(sec.Entries))
		for k, v := range sec.Entries {
			entries[k] = v
		}

		content := &sectionContent{
			comment: sec.Comment,
			entries: entries,
		}

		indexedSection[len(indexedSection)] = content
		orderedSections = append(orderedSections, section{
			name:    sec.Name,
			content: content,
		})
	}

	var currentIndexedSection map[int]*sectionContent
	var currentIndex int
	// We set the changed flag to true for our first iteration.
	changed := true

	// Then, we parse the override string.
	for {
		loc := parser.FindStringSubmatchIndex(confOverride)
		if loc == nil {
			break
		}

		if loc[2] > 0 {
			// If a section is defined in the override but has no key, remove its entries.
			if !changed {
				(*currentIndexedSection[currentIndex]).entries = make(map[string]string)
			}

			changed = false
			currentSectionName := strings.TrimSpace(confOverride[loc[2]:loc[3]])

			if loc[4] > 0 {
				i, err := strconv.Atoi(confOverride[loc[4]:loc[5]])
				if err != nil || i < 0 {
					panic("failed to parse index")
				}

				currentIndex = i
			} else {
				currentIndex = 0
			}

			var ok bool
			currentIndexedSection, ok = indexedSections[currentSectionName]
			if !ok {
				// If there is no section with this name, we are creating a new section.
				currentIndexedSection = map[int]*sectionContent{}
				indexedSections[currentSectionName] = currentIndexedSection
			}

			_, ok = currentIndexedSection[currentIndex]
			if !ok {
				// If there is no section with this index, we are creating a new section.
				emptyContent := &sectionContent{entries: make(map[string]string)}
				currentIndexedSection[currentIndex] = emptyContent
				indexedSections[currentSectionName] = currentIndexedSection
				orderedSections = append(orderedSections, section{
					name:    currentSectionName,
					content: emptyContent,
				})
			}
		} else {
			entryKey := strings.TrimSpace(confOverride[loc[6]:loc[7]])
			var value string

			if loc[8] > 0 {
				// quoted value
				value = confOverride[loc[8]:loc[9]]
			} else {
				// unquoted value
				value = strings.TrimSpace(confOverride[loc[10]:loc[11]])
			}

			changed = true
			if value == "" {
				// If the value associated to this key is empty, delete the key.
				delete((*currentIndexedSection[currentIndex]).entries, entryKey)
			} else {
				(*currentIndexedSection[currentIndex]).entries[entryKey] = value
			}
		}

		confOverride = confOverride[loc[1]:]
	}

	// Same as above, if a section is defined in the override but has no key, remove its entries.
	if !changed {
		(*currentIndexedSection[currentIndex]).entries = make(map[string]string)
	}

	res := []cfg.Section{}
	for _, orderedSection := range orderedSections {
		if len((*orderedSection.content).entries) > 0 {
			res = append(res, cfg.Section{
				Name:    orderedSection.name,
				Comment: (*orderedSection.content).comment,
				Entries: (*orderedSection.content).entries,
			})
		}
	}

	return res
}
