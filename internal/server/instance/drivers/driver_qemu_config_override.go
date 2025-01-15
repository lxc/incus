package drivers

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
)

const pattern = `\s*(?m:(?:\[([^\]]+)\](?:\[(\d+)\])?)|(?:([^=]+)[ \t]*=[ \t]*(?:"([^"]*)"|([^\n]*)))$)`

var parser = regexp.MustCompile(pattern)

type rawConfigKey struct {
	sectionName string
	index       uint
	entryKey    string
}

type configMap map[rawConfigKey]string

func sortedConfigKeys(confMap configMap) []rawConfigKey {
	rv := []rawConfigKey{}

	for k := range confMap {
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

func updateEntries(entries []cfg.Entry, sk rawConfigKey, confMap configMap) []cfg.Entry {
	rv := []cfg.Entry{}

	for _, entry := range entries {
		newEntry := cfg.Entry{
			Key:   entry.Key,
			Value: entry.Value,
		}

		ek := rawConfigKey{sk.sectionName, sk.index, entry.Key}
		val, ok := confMap[ek]
		if ok {
			// override
			delete(confMap, ek)
			newEntry.Value = val
		}

		rv = append(rv, newEntry)
	}

	return rv
}

func appendEntries(entries []cfg.Entry, sk rawConfigKey, confMap configMap) []cfg.Entry {
	// sort to have deterministic output in the appended entries
	sortedKeys := sortedConfigKeys(confMap)
	// processed all modifications for the current section, now
	// handle new entries
	for _, rawKey := range sortedKeys {
		if rawKey.sectionName != sk.sectionName || rawKey.index != sk.index {
			continue
		}

		newEntry := cfg.Entry{
			Key:   rawKey.entryKey,
			Value: confMap[rawKey],
		}

		entries = append(entries, newEntry)
		delete(confMap, rawKey)
	}

	return entries
}

func updateSections(conf []cfg.Section, confMap configMap) []cfg.Section {
	newConf := []cfg.Section{}
	sectionCounts := map[string]uint{}

	for _, section := range conf {
		count, ok := sectionCounts[section.Name]

		if ok {
			sectionCounts[section.Name] = count + 1
		} else {
			sectionCounts[section.Name] = 1
		}

		index := sectionCounts[section.Name] - 1
		sk := rawConfigKey{section.Name, index, ""}

		val, ok := confMap[sk]
		if ok {
			if val == "" {
				// deleted section
				delete(confMap, sk)
				continue
			}
		}

		newSection := cfg.Section{
			Name:    section.Name,
			Comment: section.Comment,
		}

		newSection.Entries = updateEntries(section.Entries, sk, confMap)
		newSection.Entries = appendEntries(newSection.Entries, sk, confMap)

		newConf = append(newConf, newSection)
	}

	return newConf
}

func appendSections(newConf []cfg.Section, confMap configMap) []cfg.Section {
	tmp := map[rawConfigKey]cfg.Section{}
	// sort to have deterministic output in the appended entries
	sortedKeys := sortedConfigKeys(confMap)

	for _, k := range sortedKeys {
		if k.entryKey == "" {
			// makes no sense to process section deletions (the only case where
			// entryKey == "") since we are only adding new sections now
			continue
		}

		sectionKey := rawConfigKey{k.sectionName, k.index, ""}
		section, found := tmp[sectionKey]
		if !found {
			section = cfg.Section{
				Name: k.sectionName,
			}
		}
		section.Entries = append(section.Entries, cfg.Entry{
			Key:   k.entryKey,
			Value: confMap[k],
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
		newConf = append(newConf, tmp[rawSection])
	}

	return newConf
}

func qemuRawCfgOverride(conf []cfg.Section, expandedConfig map[string]string) []cfg.Section {
	confOverride, ok := expandedConfig["raw.qemu.conf"]
	if !ok {
		return conf
	}

	confMap := parseConfOverride(confOverride)

	if len(confMap) == 0 {
		// If no keys are found, we return the conf unmodified.
		return conf
	}

	newConf := updateSections(conf, confMap)
	newConf = appendSections(newConf, confMap)

	return newConf
}
