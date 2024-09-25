package metadata

import (
	"embed"
	"encoding/json"
)

var Data map[string]any

//go:embed configuration.json
var generatedDoc embed.FS

func init() {
	file, err := generatedDoc.ReadFile("configuration.json")
	if err != nil {
		panic(err)
	}

	var data map[string]any
	err = json.Unmarshal(file, &data)
	if err != nil {
		panic(err)
	}

	Data = data
}
