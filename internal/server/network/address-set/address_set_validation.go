package addressset

import (
	"errors"
	"fmt"

	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

// ValidName checks the address set name is valid.
func ValidName(name string) error {
	if name == "" {
		return errors.New("Name is required")
	}

	// Don't allow address set names to start with dollar or arobase.
	if util.StringHasPrefix(name, "@", "$") {
		return fmt.Errorf("Name cannot start with reserved character %q", name[0])
	}

	err := validate.IsHostname(name)
	if err != nil {
		return err
	}

	return nil
}
