package main

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

var logger *log.Logger
var logFilePath string = "/tmp/incusdoc.log"

func init() {
	file, err := os.Create(logFilePath)
	if err != nil {
		log.Fatal(err)
	}

	logger = log.New(file, "INCUSDOC: ", log.Ldate|log.Ltime|log.Lshortfile)
}

var exclude []string
var yamlOutput string
var txtOutput string
var rootCmd = &cobra.Command{
	Use:   "incus-doc",
	Short: "incus-doc - a simple tool to generate documentation for Incus",
	Long:  "incus-doc - a simple tool to generate documentation for Incus. It outputs a YAML and a Markdown file that contain the content of all `gendoc:generate` statements in the project.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			log.Fatal("Please provide a path to the project")
		}

		path := args[0]
		_, err := parse(path, yamlOutput, exclude)
		if err != nil {
			log.Fatal(err)
		}

		if txtOutput != "" {
			err = writeDocFile(yamlOutput, txtOutput)
			if err != nil {
				log.Fatal(err)
			}
		}
	},
}

func main() {
	rootCmd.Flags().StringSliceVarP(&exclude, "exclude", "e", []string{}, "Path to exclude from the process")
	rootCmd.Flags().StringVarP(&yamlOutput, "yaml", "y", "incus-doc.yaml", "Output YAML file containing the generated documentation")
	rootCmd.Flags().StringVarP(&txtOutput, "txt", "t", "", "Output TXT file containing the generated documentation")
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "incus-doc failed: %v", err)
		os.Exit(1)
	}

	log.Println("incus-doc finished successfully")
}
