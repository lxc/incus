package linux

import (
	"fmt"

	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/subprocess"
)

// LoadModule loads the kernel module with the given name, by invoking
// modprobe. This respects any modprobe configuration on the system.
func LoadModule(module string) error {
	if shared.PathExists(fmt.Sprintf("/sys/module/%s", module)) {
		return nil
	}

	_, err := subprocess.RunCommand("modprobe", "-b", module)
	return err
}
