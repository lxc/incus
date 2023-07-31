package project_test

import (
	"fmt"

	"github.com/canonical/lxd/lxd/project"
)

func ExampleInstance() {
	prefixed := project.Instance(project.Default, "test")
	fmt.Println(prefixed)

	prefixed = project.Instance("project_name", "test1")
	fmt.Println(prefixed)
	// Output: test
	// project_name_test1
}

func ExampleInstanceParts() {
	projectName, name := project.InstanceParts("unprefixed")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Instance(project.Default, "test"))
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
	prefixed := project.StorageVolume(project.Default, "test")
	fmt.Println(prefixed)

	prefixed = project.StorageVolume("project_name", "test1")
	fmt.Println(prefixed)
	// Output: default_test
	// project_name_test1
}
