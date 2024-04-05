package main

import (
	"encoding/json"
	"os"

	"github.com/lxc/incus/v6/shared/simplestreams"
)

func writeIndex(products *simplestreams.Products) error {
	// Update the product list.
	productNames := make([]string, 0, len(products.Products))
	for name := range products.Products {
		productNames = append(productNames, name)
	}

	// Write a new index file.
	stream := simplestreams.Stream{
		Format: "index:1.0",
		Index: map[string]simplestreams.StreamIndex{
			"images": {
				DataType: "image-downloads",
				Path:     "streams/v1/images.json",
				Format:   "products:1.0",
				Products: productNames,
			},
		},
	}

	body, err := json.Marshal(&stream)
	if err != nil {
		return err
	}

	err = os.WriteFile("streams/v1/index.json", body, 0644)
	if err != nil {
		return err
	}

	return nil
}
