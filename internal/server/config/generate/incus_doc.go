package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lxc/incus/shared/util"
)

var (
	globalGenDocRegex   = regexp.MustCompile(`(?m)gendoc:generate\((.*)\)([\S\s]+)\s+---\n([\S\s]+)`)
	genDocMetadataRegex = regexp.MustCompile(`(?m)([^,\s]+)=([^,\s]+)`)
	genDocDataRegex     = regexp.MustCompile(`(?m)([\S]+):[\s]+([\S \"\']+)`)
)

var mdKeys []string = []string{"entity", "group", "key"}

// IterableAny is a generic type that represents a type or an iterable container.
type IterableAny interface {
	any | []any
}

// doc is the structure of the JSON file that contains the generated configuration metadata.
type doc struct {
	Configs map[string]any `json:"configs"`
}

// sortConfigKeys alphabetically sorts the entries by key (config option key) within each config group in an entity.
func sortConfigKeys(projectEntries map[string]any) {
	for _, entityValue := range projectEntries {
		for _, groupValue := range entityValue.(map[string]any) {
			configEntries := groupValue.(map[string]any)["keys"].([]any)
			sort.Slice(configEntries, func(i, j int) bool {
				// Get the only key for each map element in the slice
				var keyI, keyJ string

				confI, confJ := configEntries[i].(map[string]any), configEntries[j].(map[string]any)
				for k := range confI {
					keyI = k
					break // There is only one key-value pair in each map
				}

				for k := range confJ {
					keyJ = k
					break // There is only one key-value pair in each map
				}

				// Compare the keys
				return keyI < keyJ
			})
		}
	}
}

// getSortedKeysFromMap returns the keys of a map sorted alphabetically.
func getSortedKeysFromMap[K string, V IterableAny](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func parse(path string, outputJSONPath string, excludedPaths []string) (*doc, error) {
	jsonDoc := &doc{}
	docKeys := make(map[string]struct{}, 0)
	projectEntries := make(map[string]any)
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip excluded paths
		if util.ValueInSlice(path, excludedPaths) {
			if info.IsDir() {
				log.Printf("Skipping excluded directory: %v", path)
				return filepath.SkipDir
			}

			log.Printf("Skipping excluded file: %v", path)
			return nil
		}

		// Only process go files
		if !info.IsDir() && filepath.Ext(path) != ".go" {
			return nil
		}

		// Continue walking if directory
		if info.IsDir() {
			return nil
		}

		// Parse file and create the AST
		var fset = token.NewFileSet()
		var f *ast.File
		f, err = parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		fileEntries := make([]map[string]any, 0)

		// Loop in comment groups
		for _, cg := range f.Comments {
			s := cg.Text()
			entry := make(map[string]any)
			groupKeyEntry := make(map[string]any)
			for _, match := range globalGenDocRegex.FindAllStringSubmatch(s, -1) {
				// check that the match contains the expected number of groups
				if len(match) != 4 {
					continue
				}

				log.Printf("Found gendoc at %s", fset.Position(cg.Pos()).String())
				metadata := match[1]
				longdesc := match[2]
				data := match[3]
				// process metadata
				metadataMap := make(map[string]string)
				var entityKey string
				var groupKey string
				var simpleKey string
				for _, mdKVMatch := range genDocMetadataRegex.FindAllStringSubmatch(metadata, -1) {
					if len(mdKVMatch) != 3 {
						continue
					}

					mdKey := mdKVMatch[1]
					mdValue := mdKVMatch[2]
					// check that the metadata key is among the expected ones
					if !util.ValueInSlice(mdKey, mdKeys) {
						continue
					}

					if mdKey == "entity" {
						entityKey = mdValue
					}

					if mdKey == "group" {
						groupKey = mdValue
					}

					if mdKey == "key" {
						simpleKey = mdValue
					}

					metadataMap[mdKey] = mdValue
				}

				// Check that this metadata is not already present
				mdKeyHash := fmt.Sprintf("%s/%s/%s", entityKey, groupKey, simpleKey)
				_, ok := docKeys[mdKeyHash]
				if ok {
					return fmt.Errorf("Duplicate key '%s' found at %s", mdKeyHash, fset.Position(cg.Pos()).String())
				}

				docKeys[mdKeyHash] = struct{}{}

				configKeyEntry := make(map[string]any)
				configKeyEntry[metadataMap["key"]] = make(map[string]any)
				configKeyEntry[metadataMap["key"]].(map[string]any)["longdesc"] = strings.TrimLeft(longdesc, "\n\t\v\f\r")
				for _, dataKVMatch := range genDocDataRegex.FindAllStringSubmatch(data, -1) {
					if len(dataKVMatch) != 3 {
						continue
					}

					configKeyEntry[metadataMap["key"]].(map[string]any)[dataKVMatch[1]] = dataKVMatch[2]
				}

				_, ok = groupKeyEntry[metadataMap["group"]]
				if ok {
					_, ok = groupKeyEntry[metadataMap["group"]].(map[string]any)["keys"]
					if ok {
						groupKeyEntry[metadataMap["group"]].(map[string]any)["keys"] = append(
							groupKeyEntry[metadataMap["group"]].(map[string]any)["keys"].([]any),
							configKeyEntry,
						)
					} else {
						groupKeyEntry[metadataMap["group"]].(map[string]any)["keys"] = []any{configKeyEntry}
					}
				} else {
					groupKeyEntry[metadataMap["group"]] = make(map[string]any)
					groupKeyEntry[metadataMap["group"]].(map[string]any)["keys"] = []any{configKeyEntry}
				}

				entry[metadataMap["entity"]] = groupKeyEntry
			}

			if len(entry) > 0 {
				fileEntries = append(fileEntries, entry)
			}
		}

		// Update projectEntries
		for _, entry := range fileEntries {
			for entityKey, entityValue := range entry {
				_, ok := projectEntries[entityKey]
				if !ok {
					projectEntries[entityKey] = entityValue
				} else {
					for groupKey, groupValue := range entityValue.(map[string]any) {
						_, ok := projectEntries[entityKey].(map[string]any)[groupKey]
						if !ok {
							projectEntries[entityKey].(map[string]any)[groupKey] = groupValue
						} else {
							// merge the config keys
							configKeys := groupValue.(map[string]any)["keys"].([]any)
							projectEntries[entityKey].(map[string]any)[groupKey].(map[string]any)["keys"] = append(
								projectEntries[entityKey].(map[string]any)[groupKey].(map[string]any)["keys"].([]any),
								configKeys...,
							)
						}
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// sort the config keys alphabetically
	sortConfigKeys(projectEntries)
	jsonDoc.Configs = projectEntries
	data, err := json.MarshalIndent(jsonDoc, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("Error while marshaling project documentation: %v", err)
	}

	if outputJSONPath != "" {
		buf := bytes.NewBufferString("")
		_, err = buf.Write(data)
		if err != nil {
			return nil, fmt.Errorf("Error while writing the JSON project documentation: %v", err)
		}

		err := os.WriteFile(outputJSONPath, buf.Bytes(), 0644)
		if err != nil {
			return nil, fmt.Errorf("Error while writing the JSON project documentation: %v", err)
		}
	}

	return jsonDoc, nil
}

func writeDocFile(inputJSONPath, outputTxtPath string) error {
	countMaxBackTicks := func(s string) int {
		count, curr_count := 0, 0
		n := len(s)
		for i := 0; i < n; i++ {
			if s[i] == '`' {
				curr_count++
				continue
			}

			if curr_count > count {
				count = curr_count
			}

			curr_count = 0
		}

		return count
	}

	specialChars := []string{"", "*", "_", "#", "+", "-", ".", "!", "no", "yes"}

	// read the JSON file which is the source of truth for the generation of the .txt file
	jsonData, err := os.ReadFile(inputJSONPath)
	if err != nil {
		return err
	}

	var jsonDoc doc

	err = json.Unmarshal(jsonData, &jsonDoc)
	if err != nil {
		return err
	}

	sortedEntityKeys := getSortedKeysFromMap(jsonDoc.Configs)
	// create a string buffer
	buffer := bytes.NewBufferString("// Code generated by incus-doc; DO NOT EDIT.\n\n")
	for _, entityKey := range sortedEntityKeys {
		entityEntries := jsonDoc.Configs[entityKey]
		sortedGroupKeys := getSortedKeysFromMap(entityEntries.(map[string]any))
		for _, groupKey := range sortedGroupKeys {
			groupEntries := entityEntries.(map[string]any)[groupKey]
			buffer.WriteString(fmt.Sprintf("<!-- config group %s-%s start -->\n", entityKey, groupKey))
			for _, configEntry := range groupEntries.(map[string]any)["keys"].([]any) {
				for configKey, configContent := range configEntry.(map[string]any) {
					// There is only one key-value pair in each map
					kvBuffer := bytes.NewBufferString("")
					var backticksCount int
					var longDescContent string
					sortedConfigContentKeys := getSortedKeysFromMap(configContent.(map[string]any))
					for _, configEntryContentKey := range sortedConfigContentKeys {
						configContentValue := configContent.(map[string]any)[configEntryContentKey]
						if configEntryContentKey == "longdesc" {
							backticksCount = countMaxBackTicks(configContentValue.(string))
							longDescContent = configContentValue.(string)
							continue
						}

						configContentValueStr, ok := configContentValue.(string)
						if ok {
							if (strings.HasSuffix(configContentValueStr, "`") && strings.HasPrefix(configContentValueStr, "`")) || util.ValueInSlice(configContentValueStr, specialChars) {
								configContentValueStr = fmt.Sprintf("\"%s\"", configContentValueStr)
							}
						} else {
							switch configEntryContentTyped := configContentValue.(type) {
							case int, float64, bool:
								configContentValueStr = fmt.Sprint(configEntryContentTyped)
							case time.Time:
								configContentValueStr = fmt.Sprint(configEntryContentTyped.Format(time.RFC3339))
							}
						}

						var quoteFormattedValue string
						if strings.Contains(configContentValueStr, `"`) {
							if strings.HasPrefix(configContentValueStr, `"`) && strings.HasSuffix(configContentValueStr, `"`) {
								for i, s := range configContentValueStr[1 : len(configContentValueStr)-1] {
									if s == '"' {
										_ = strings.Replace(configContentValueStr, `"`, `\"`, i)
									}
								}
								quoteFormattedValue = configContentValueStr
							} else {
								quoteFormattedValue = strings.ReplaceAll(configContentValueStr, `"`, `\"`)
							}
						} else {
							quoteFormattedValue = fmt.Sprintf("\"%s\"", configContentValueStr)
						}

						kvBuffer.WriteString(
							fmt.Sprintf(
								":%s: %s\n",
								configEntryContentKey,
								quoteFormattedValue,
							),
						)
					}

					if backticksCount < 3 {
						buffer.WriteString(
							fmt.Sprintf("```{config:option} %s %s-%s\n%s%s\n```\n\n",
								configKey,
								entityKey,
								groupKey,
								kvBuffer.String(),
								strings.TrimLeft(longDescContent, "\n"),
							))
					} else {
						configQuotes := strings.Repeat("`", backticksCount+1)
						buffer.WriteString(
							fmt.Sprintf("%s{config:option} %s %s-%s\n%s%s\n%s\n\n",
								configQuotes,
								configKey,
								entityKey,
								groupKey,
								kvBuffer.String(),
								strings.TrimLeft(longDescContent, "\n"),
								configQuotes,
							))
					}
				}
			}

			buffer.WriteString(fmt.Sprintf("<!-- config group %s-%s end -->\n", entityKey, groupKey))
		}
	}

	err = os.WriteFile(outputTxtPath, buffer.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("Error while writing the Markdown project documentation: %v", err)
	}

	return nil
}
