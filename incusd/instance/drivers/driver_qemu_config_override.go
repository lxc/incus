package drivers

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const pattern = `\s*(?m:(?:\[([^\]]+)\](?:\[(\d+)\])?)|(?:([^=]+)[ \t]*=[ \t]*(?:"([^"]*)"|([^\n]*)))$)`

var parser = regexp.MustCompile(pattern)

type rawConfigKey struct {
	sectionName string
	index       uint
	entryKey    string
}

type configMap map[rawConfigKey]string

func sortedConfigKeys(cfgMap configMap) []rawConfigKey {
	rv := []rawConfigKey{}

	for k := range cfgMap {
		rv = append(rv, k)
	}

	sort.SliceStable(rv, func(i, j int) bool {
		return rv[i].sectionName < rv[j].sectionName ||
			rv[i].index < rv[j].index ||
			rv[i].entryKey < rv[j].entryKey
	})

	return rv
}

func parseConfOverride(confOverride string) configMap {
	s := confOverride
	rv := configMap{}
	currentSectionName := ""
	var currentIndex uint
	currentEntryCount := 0

	for {
		loc := parser.FindStringSubmatchIndex(s)
		if loc == nil {
			break
		}

		if loc[2] > 0 {
			if currentSectionName != "" && currentEntryCount == 0 {
				// new section started and previous section ended without entries
				k := rawConfigKey{
					sectionName: currentSectionName,
					index:       currentIndex,
					entryKey:    "",
				}

				rv[k] = ""
			}

			currentEntryCount = 0
			currentSectionName = strings.TrimSpace(s[loc[2]:loc[3]])
			if loc[4] > 0 {
				i, err := strconv.Atoi(s[loc[4]:loc[5]])
				if err != nil || i < 0 {
					panic("failed to parse index")
				}

				currentIndex = uint(i)
			} else {
				currentIndex = 0
			}
		} else {
			entryKey := strings.TrimSpace(s[loc[6]:loc[7]])
			var value string

			if loc[8] > 0 {
				// quoted value
				value = s[loc[8]:loc[9]]
			} else {
				// unquoted value
				value = strings.TrimSpace(s[loc[10]:loc[11]])
			}

			k := rawConfigKey{
				sectionName: currentSectionName,
				index:       currentIndex,
				entryKey:    entryKey,
			}

			rv[k] = value
			currentEntryCount++
		}

		s = s[loc[1]:]
	}

	if currentSectionName != "" && currentEntryCount == 0 {
		// previous section ended without entries
		k := rawConfigKey{
			sectionName: currentSectionName,
			index:       currentIndex,
			entryKey:    "",
		}

		rv[k] = ""
	}

	return rv
}

func updateEntries(entries []cfgEntry, sk rawConfigKey, cfgMap configMap) []cfgEntry {
	rv := []cfgEntry{}

	for _, entry := range entries {
		newEntry := cfgEntry{
			key:   entry.key,
			value: entry.value,
		}

		ek := rawConfigKey{sk.sectionName, sk.index, entry.key}
		val, ok := cfgMap[ek]
		if ok {
			// override
			delete(cfgMap, ek)
			newEntry.value = val
		}

		rv = append(rv, newEntry)
	}

	return rv
}

func appendEntries(entries []cfgEntry, sk rawConfigKey, cfgMap configMap) []cfgEntry {
	// sort to have deterministic output in the appended entries
	sortedKeys := sortedConfigKeys(cfgMap)
	// processed all modifications for the current section, now
	// handle new entries
	for _, rawKey := range sortedKeys {
		if rawKey.sectionName != sk.sectionName || rawKey.index != sk.index {
			continue
		}

		newEntry := cfgEntry{
			key:   rawKey.entryKey,
			value: cfgMap[rawKey],
		}

		entries = append(entries, newEntry)
		delete(cfgMap, rawKey)
	}

	return entries
}

func updateSections(cfg []cfgSection, cfgMap configMap) []cfgSection {
	newCfg := []cfgSection{}
	sectionCounts := map[string]uint{}

	for _, section := range cfg {
		count, ok := sectionCounts[section.name]

		if ok {
			sectionCounts[section.name] = count + 1
		} else {
			sectionCounts[section.name] = 1
		}

		index := sectionCounts[section.name] - 1
		sk := rawConfigKey{section.name, index, ""}

		val, ok := cfgMap[sk]
		if ok {
			if val == "" {
				// deleted section
				delete(cfgMap, sk)
				continue
			}
		}

		newSection := cfgSection{
			name:    section.name,
			comment: section.comment,
		}

		newSection.entries = updateEntries(section.entries, sk, cfgMap)
		newSection.entries = appendEntries(newSection.entries, sk, cfgMap)

		newCfg = append(newCfg, newSection)
	}

	return newCfg
}

func appendSections(newCfg []cfgSection, cfgMap configMap) []cfgSection {
	tmp := map[rawConfigKey]cfgSection{}
	// sort to have deterministic output in the appended entries
	sortedKeys := sortedConfigKeys(cfgMap)

	for _, k := range sortedKeys {
		if k.entryKey == "" {
			// makes no sense to process section deletions (the only case where
			// entryKey == "") since we are only adding new sections now
			continue
		}

		sectionKey := rawConfigKey{k.sectionName, k.index, ""}
		section, found := tmp[sectionKey]
		if !found {
			section = cfgSection{
				name: k.sectionName,
			}
		}
		section.entries = append(section.entries, cfgEntry{
			key:   k.entryKey,
			value: cfgMap[k],
		})
		tmp[sectionKey] = section
	}

	rawSections := []rawConfigKey{}
	for rawSection := range tmp {
		rawSections = append(rawSections, rawSection)
	}

	// Sort to have deterministic output in the appended sections
	sort.SliceStable(rawSections, func(i, j int) bool {
		return rawSections[i].sectionName < rawSections[j].sectionName ||
			rawSections[i].index < rawSections[j].index
	})

	for _, rawSection := range rawSections {
		newCfg = append(newCfg, tmp[rawSection])
	}

	return newCfg
}

func qemuRawCfgOverride(cfg []cfgSection, expandedConfig map[string]string) []cfgSection {
	confOverride, ok := expandedConfig["raw.qemu.conf"]
	if !ok {
		return cfg
	}

	cfgMap := parseConfOverride(confOverride)

	if len(cfgMap) == 0 {
		// If no keys are found, we return the cfg unmodified.
		return cfg
	}

	newCfg := updateSections(cfg, cfgMap)
	newCfg = appendSections(newCfg, cfgMap)

	return newCfg
}
