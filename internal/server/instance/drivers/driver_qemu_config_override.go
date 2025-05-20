package drivers

import (
	"bufio"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
)

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
func qemuRawCfgOverride(conf []cfg.Section, confOverride string) ([]cfg.Section, error) {
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
		maps.Copy(entries, sec.Entries)

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
	var currentSectionName string
	// We set the changed flag to true for our first iteration.
	changed := true

	scanner := bufio.NewScanner(strings.NewReader(confOverride))

	// Then, we parse the override string.
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		length := len(line)
		if length == 0 {
			continue
		}

		if strings.HasPrefix(line, "[") {
			// Find closing `]` for section name.
			end := strings.IndexByte(line, ']')
			if end <= 1 {
				return nil, fmt.Errorf("Invalid section header (must be a section name enclosed in square brackets): %q", line)
			}

			// If a section is defined in the override but has no key, remove its entries.
			if !changed {
				(*currentIndexedSection[currentIndex]).entries = make(map[string]string)
			}

			changed = false
			currentSectionName = strings.TrimSpace(line[1:end])
			currentIndex = 0
			if length > end+1 {
				// Optional section index
				rest := line[end+1:]
				e := fmt.Errorf("Invalid section index (must be an integer enclosed in square brackets): %q", rest)
				if !strings.HasPrefix(rest, "[") || !strings.HasSuffix(rest, "]") {
					return nil, e
				}

				var err error
				currentIndex, err = strconv.Atoi(rest[1 : len(rest)-1])
				if err != nil {
					return nil, e
				}
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
			if currentSectionName == "" {
				return nil, fmt.Errorf("Expected section header, got: %q", line)
			}

			eqLoc := strings.IndexByte(line, '=')
			if eqLoc < 1 {
				return nil, fmt.Errorf("Invalid property override line (must be `key=value`): %q", line)
			}

			key := strings.TrimSpace(line[:eqLoc])
			value := strings.TrimSpace(line[eqLoc+1:])
			if strings.HasPrefix(value, "\"") {
				// We are dealing with a quoted value.
				var err error
				value, err = strconv.Unquote(value)
				if err != nil {
					return nil, fmt.Errorf("Invalid quoted value: %q", value)
				}
			}

			changed = true
			if value == "" {
				// If the value associated to this key is empty, delete the key.
				delete((*currentIndexedSection[currentIndex]).entries, key)
			} else {
				(*currentIndexedSection[currentIndex]).entries[key] = value
			}
		}
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

	return res, nil
}
