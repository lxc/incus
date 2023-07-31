package acl

import (
	"fmt"

	"github.com/cyphar/incus/shared"
	"github.com/cyphar/incus/shared/validate"
)

// ValidName checks the ACL name is valid.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("Name is required")
	}

	// Don't allow ACL names to start with special port selector characters to allow LXD to define special port
	// selectors without risking conflict with user defined ACL names.
	if shared.StringHasPrefix(name, "@", "%", "#") {
		return fmt.Errorf("Name cannot start with reserved character %q", name[0])
	}

	// Ensures we can differentiate an ACL name from an IP in rules that reference this ACL.
	err := validate.IsHostname(name)
	if err != nil {
		return err
	}

	return nil
}
