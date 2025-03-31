package revert_test

import (
	"fmt"

	"github.com/lxc/incus/v6/shared/revert"
)

func ExampleReverter_fail() {
	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() { fmt.Println("1st step") })
	reverter.Add(func() { fmt.Println("2nd step") })

	// Revert functions are run in reverse order on return.
	// Output: 2nd step
	// 1st step
}

func ExampleReverter_success() {
	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() { fmt.Println("1st step") })
	reverter.Add(func() { fmt.Println("2nd step") })

	reverter.Success() // Revert functions added are not run on return.
	// Output:
}
