package cmd

// Usage formats the command name and sub-command name.
func Usage(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}

	return name + " " + args[0]
}
