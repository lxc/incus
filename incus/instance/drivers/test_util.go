package drivers

import (
	"github.com/cyphar/incus/lxd/instance"
	"github.com/cyphar/incus/lxd/instance/instancetype"
)

// PrepareEqualTest modifies any unexported variables required for reflect.DeepEqual to complete safely.
// This is used for tests to avoid infinite recursion loops.
func PrepareEqualTest(insts ...instance.Instance) {
	for _, inst := range insts {
		if inst.Type() == instancetype.Container {
			// When loading from DB, we won't have a full LXC config.
			inst.(*lxc).c = nil
			inst.(*lxc).cConfig = false
		}
	}
}
