package main

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

var logger *log.Logger
var logFilePath string = "/tmp/lxddoc.log"

func init() {
	file, err := os.Create(logFilePath)
	if err != nil {
		log.Fatal(err)
	}

	logger = log.New(file, "LXDDOC: ", log.Ldate|log.Ltime|log.Lshortfile)
}

var exclude []string
var yamlOutput string
var txtOutput string
var rootCmd = &cobra.Command{
	Use:   "lxd-doc",
	Short: "lxd-doc - a simple tool to generate documentation for LXD",
	Long:  "lxd-doc - a simple tool to generate documentation for LXD. It outputs a YAML and a Markdown file that contain the content of all `lxddoc:generate` statements in the project.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			log.Fatal("Please provide a path to the project")
		}

		path := args[0]
		yaml, err := parse(path, yamlOutput, exclude)
		if err != nil {
			log.Fatal(err)
		}

		err = writeDocFile(txtOutput, yaml)
		if err != nil {
			log.Fatal(err)
		}
	},
}

func main() {
	rootCmd.Flags().StringSliceVarP(&exclude, "exclude", "e", []string{}, "Path to exclude from the process")
	rootCmd.Flags().StringVarP(&yamlOutput, "yaml", "y", "lxd-doc.yaml", "Output YAML file containing the generated documentation")
	rootCmd.Flags().StringVarP(&txtOutput, "txt", "t", "lxd-doc.txt", "Output TXT file containing the generated documentation")
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lxd-doc failed: %v", err)
		os.Exit(1)
	}

	log.Println("lxd-doc finished successfully")
}
