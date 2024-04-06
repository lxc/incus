package validate_test

import (
	"fmt"

	"github.com/lxc/incus/v6/shared/validate"
)

func ExampleIsNetworkMAC() {
	tests := []string{
		"00:00:5e:00:53:01",
		"02:00:5e:10:00:00:00:01", // too long
		"00-00-5e-00-53-01",       // invalid delimiter
		"0000.5e00.5301",          // invalid delimiter
		"invalid",
		"",
	}

	for _, v := range tests {
		err := validate.IsNetworkMAC(v)
		fmt.Printf("%s, %t\n", v, err == nil)
	}

	// Output: 00:00:5e:00:53:01, true
	// 02:00:5e:10:00:00:00:01, false
	// 00-00-5e-00-53-01, false
	// 0000.5e00.5301, false
	// invalid, false
	// , false
}

func ExampleIsPCIAddress() {
	tests := []string{
		"0000:12:ab.0", // valid
		"0010:12:ab.0", // valid
		"0000:12:CD.0", // valid
		"12:ab.0",      // valid
		"12:CD.0",      // valid
		"0000:12:gh.0", // invalid hex
		"0000:12:GH.0", // invalid hex
		"12:gh.0",      // invalid hex
		"12:GH.0",      // invalid hex
		"000:12:CD.0",  // wrong prefix
		"12.ab.0",      // invalid format
		"",
	}

	for _, v := range tests {
		err := validate.IsPCIAddress(v)
		fmt.Printf("%s, %t\n", v, err == nil)
	}

	// Output: 0000:12:ab.0, true
	// 0010:12:ab.0, true
	// 0000:12:CD.0, true
	// 12:ab.0, true
	// 12:CD.0, true
	// 0000:12:gh.0, false
	// 0000:12:GH.0, false
	// 12:gh.0, false
	// 12:GH.0, false
	// 000:12:CD.0, false
	// 12.ab.0, false
	// , false
}

func ExampleOptional() {
	tests := []string{
		"",
		"foo",
		"true",
	}

	for _, v := range tests {
		f := validate.Optional()
		fmt.Printf("%v ", f(v))

		f = validate.Optional(validate.IsBool)
		fmt.Printf("%v\n", f(v))
	}

	// Output: <nil> <nil>
	// <nil> Invalid value for a boolean "foo"
	// <nil> <nil>
}

func ExampleRequired() {
	tests := []string{
		"",
		"foo",
		"true",
	}

	for _, v := range tests {
		f := validate.Required()
		fmt.Printf("%v ", f(v))

		f = validate.Required(validate.IsBool)
		fmt.Printf("%v\n", f(v))
	}

	// Output: <nil> Invalid value for a boolean ""
	// <nil> Invalid value for a boolean "foo"
	// <nil> <nil>
}

func ExampleIsValidCPUSet() {
	tests := []string{
		"1",       // valid
		"1,2,3",   // valid
		"1-3",     // valid
		"1-3,4-6", // valid
		"1-3,4",   // valid
		"abc",     // invalid syntax
		"1-",      // invalid syntax
		"1,",      // invalid syntax
		"-1",      // invalid syntax
		",1",      // invalid syntax
		"1,2,3,3", // invalid: Duplicate CPU
		"1-2,2",   // invalid: Duplicate CPU
		"1-2,2-3", // invalid: Duplicate CPU
	}

	for _, t := range tests {
		err := validate.IsValidCPUSet(t)
		fmt.Printf("%v\n", err)
	}

	// Output: <nil>
	// <nil>
	// <nil>
	// <nil>
	// <nil>
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Invalid CPU limit syntax
	// Cannot define CPU multiple times
	// Cannot define CPU multiple times
	// Cannot define CPU multiple times
}
