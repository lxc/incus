package load

import (
	"sync"

	"go.starlark.net/starlark"
)

// nameInstancePlacement is the name used in Starlark for the instance placement scriptlet.
const nameInstancePlacement = "instance_placement"

// prefixQEMU is the prefix used in Starlark for the QEMU scriptlet.
const prefixQEMU = "qemu"

// nameAuthorization is the name used in Starlark for the Authorization scriptlet.
const nameAuthorization = "authorization"

var programsMu sync.Mutex
var programs = make(map[string]*starlark.Program)

// InstancePlacementCompile compiles the instance placement scriptlet.
func InstancePlacementCompile(name string, src string) (*starlark.Program, error) {
	return compile(name, src, []string{
		"log_info",
		"log_warn",
		"log_error",
		"set_target",
		"get_cluster_member_resources",
		"get_cluster_member_state",
		"get_instance_resources",
		"get_instances",
		"get_instances_count",
		"get_cluster_members",
		"get_project",
	})
}

// InstancePlacementValidate validates the instance placement scriptlet.
func InstancePlacementValidate(src string) error {
	return validate(InstancePlacementCompile, nameInstancePlacement, src, declaration{
		required("instance_placement"): {"request", "candidate_members"},
	})
}

// InstancePlacementSet compiles the instance placement scriptlet into memory for use with InstancePlacementRun.
// If empty src is provided the current program is deleted.
func InstancePlacementSet(src string) error {
	return set(InstancePlacementCompile, nameInstancePlacement, src)
}

// InstancePlacementProgram returns the precompiled instance placement scriptlet program.
func InstancePlacementProgram() (*starlark.Program, *starlark.Thread, error) {
	return program("Instance placement", nameInstancePlacement)
}

// QEMUCompile compiles the QEMU scriptlet.
func QEMUCompile(name string, src string) (*starlark.Program, error) {
	return compile(name, src, []string{
		"log_info",
		"log_warn",
		"log_error",
		"run_qmp",
		"run_command",
		"blockdev_add",
		"blockdev_del",
		"chardev_add",
		"chardev_change",
		"chardev_remove",
		"device_add",
		"device_del",
		"netdev_add",
		"netdev_del",
		"object_add",
		"object_del",
		"qom_get",
		"qom_list",
		"qom_set",
	})
}

// QEMUValidate validates the QEMU scriptlet.
func QEMUValidate(src string) error {
	return validate(QEMUCompile, prefixQEMU, src, declaration{
		required("qemu_hook"): {"hook_name"},
	})
}

// QEMUSet compiles the QEMU scriptlet into memory for use with QEMURun.
// If empty src is provided the current program is deleted.
func QEMUSet(src string, instance string) error {
	return set(QEMUCompile, prefixQEMU+"/"+instance, src)
}

// QEMUProgram returns the precompiled QEMU scriptlet program.
func QEMUProgram(instance string) (*starlark.Program, *starlark.Thread, error) {
	return program("QEMU", prefixQEMU+"/"+instance)
}

// AuthorizationCompile compiles the authorization scriptlet.
func AuthorizationCompile(name string, src string) (*starlark.Program, error) {
	return compile(name, src, []string{
		"log_info",
		"log_warn",
		"log_error",
	})
}

// AuthorizationValidate validates the authorization scriptlet.
func AuthorizationValidate(src string) error {
	return validate(AuthorizationCompile, nameAuthorization, src, declaration{
		required("authorize"):           {"details", "object", "entitlement"},
		optional("get_instance_access"): {"project_name", "instance_name"},
		optional("get_project_access"):  {"project_name"},
	})
}

// AuthorizationSet compiles the authorization scriptlet into memory for use with AuthorizationRun.
// If empty src is provided the current program is deleted.
func AuthorizationSet(src string) error {
	return set(AuthorizationCompile, nameAuthorization, src)
}

// AuthorizationProgram returns the precompiled authorization scriptlet program.
func AuthorizationProgram() (*starlark.Program, *starlark.Thread, error) {
	return program("Authorization", nameAuthorization)
}
