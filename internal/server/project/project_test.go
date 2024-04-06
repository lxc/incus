package project_test

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/shared/api"
)

func ExampleInstance() {
	prefixed := project.Instance(api.ProjectDefaultName, "test")
	fmt.Println(prefixed)

	prefixed = project.Instance("project_name", "test1")
	fmt.Println(prefixed)
	// Output: test
	// project_name_test1
}

func ExampleInstanceParts() {
	projectName, name := project.InstanceParts("unprefixed")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Instance(api.ProjectDefaultName, "test"))
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts("project_name_test")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Instance("proj", "test1"))
	fmt.Println(projectName, name)

	// Output: default unprefixed
	// default test
	// project_name test
	// proj test1
}

func ExampleStorageVolume() {
	prefixed := project.StorageVolume(api.ProjectDefaultName, "test")
	fmt.Println(prefixed)

	prefixed = project.StorageVolume("project_name", "test1")
	fmt.Println(prefixed)
	// Output: default_test
	// project_name_test1
}
