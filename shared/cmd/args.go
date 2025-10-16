package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// ErrBadArgs is returned when the incorrect number of arguments was passed.
var ErrBadArgs = errors.New("incorrect number of arguments")

// CheckArgs validates the number of arguments for a command.
func CheckArgs(cmd *cobra.Command, args []string, minArgs int, maxArgs int) (bool, error) {
	if len(args) < minArgs || (maxArgs != -1 && len(args) > maxArgs) {
		_ = cmd.Help()

		if len(args) == 0 {
			return true, nil
		}

		return true, errors.New("Invalid number of arguments")
	}

	return false, nil
}
