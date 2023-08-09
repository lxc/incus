package main

import (
	"os"
)

func main() {
	root := newRoot()
	err := root.Execute()
	if err != nil {
		os.Exit(1)
	}
}
