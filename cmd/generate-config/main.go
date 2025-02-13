package main

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

var exclude []string
var jsonOutput string
var txtOutput string
var rootCmd = &cobra.Command{
	Use:   "generate-config",
	Short: "generate-config - a simple tool to generate documentation for Incus",
	Long:  "generate-config - a simple tool to generate documentation for Incus. It outputs a YAML and a Markdown file that contain the content of all `gendoc:generate` statements in the project.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			log.Fatal("Please provide a path to the project")
		}

		path := args[0]
		_, err := parse(path, jsonOutput, exclude)
		if err != nil {
			log.Fatal(err)
		}

		if txtOutput != "" {
			err = writeDocFile(jsonOutput, txtOutput)
			if err != nil {
				log.Fatal(err)
			}
		}
	},
}

func main() {
	rootCmd.Flags().StringSliceVarP(&exclude, "exclude", "e", []string{}, "Path to exclude from the process")
	rootCmd.Flags().StringVarP(&jsonOutput, "json", "j", "configuration.json", "Output JSON file containing the generated configuration")
	rootCmd.Flags().StringVarP(&txtOutput, "txt", "t", "", "Output TXT file containing the generated documentation")
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate-config failed: %v", err)
		os.Exit(1)
	}

	log.Println("generate-config finished successfully")
}
