package refcount

import (
	"fmt"
)

func ExampleIncrement() {
	refCounter1 := "testinc1"
	fmt.Println(Increment(refCounter1, 1))
	fmt.Println(Increment(refCounter1, 1))
	fmt.Println(Increment(refCounter1, 2))

	refCounter2 := "testinc2"
	fmt.Println(Increment(refCounter2, 1))
	fmt.Println(Increment(refCounter2, 10))

	// Test overflow panic.
	defer func() {
		r := recover()
		if r != nil {
			fmt.Printf("Recovered: %v\n", r)
		}
	}()
	var maxUint uint = ^uint(0)
	fmt.Println(Increment(refCounter2, maxUint))

	// Output: 1
	// 2
	// 4
	// 1
	// 11
	// Recovered: Ref counter "testinc2" overflowed from 11 to 10
}

func ExampleDecrement() {
	refCounter1 := "testdec1"
	fmt.Println(Increment(refCounter1, 10))
	fmt.Println(Decrement(refCounter1, 1))
	fmt.Println(Decrement(refCounter1, 2))

	refCounter2 := "testdec2"
	fmt.Println(Decrement(refCounter2, 1))
	fmt.Println(Increment(refCounter2, 1))
	fmt.Println(Decrement(refCounter2, 1))
	fmt.Println(Decrement(refCounter2, 1))

	// Output: 10
	// 9
	// 7
	// 0
	// 1
	// 0
	// 0
}
