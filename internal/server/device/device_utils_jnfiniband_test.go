package device

import (
	"fmt"
)

func Example_infinibandValidMAC() {
	tests := []string{
		"00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01", // valid long form
		"a0:00:0f:c0:fe:80:00:00:00:00:00:00:4a:c8:f9:1b:aa:57:ef:19", // valid long form
		"02:00:5e:10:00:00:00:01",                                     // valid short form
		"4a:c8:f9:1b:aa:57:ef:19",                                     // valid short form
		"00-00-00-00-fe-80-00-00-00-00-00-00-02-00-5e-10-00-00-00-01", // invalid delimiter long form
		"0000.0000.fe80.0000.0000.0000.0200.5e10.0000.0001",           // invalid delimiter long form
		"02-00-5e-10-00-00-00-01",                                     // invalid delimiter short form
		"0200.5e10.0000.0001",                                         // invalid delimiter short form
		"00:00:5e:00:53:01",                                           // invalid ethernet MAC
		"invalid",
		"",
	}

	for _, v := range tests {
		err := infinibandValidMAC(v)
		fmt.Printf("%s, %t\n", v, err == nil)
	}

	// Output: 00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01, true
	// a0:00:0f:c0:fe:80:00:00:00:00:00:00:4a:c8:f9:1b:aa:57:ef:19, true
	// 02:00:5e:10:00:00:00:01, true
	// 4a:c8:f9:1b:aa:57:ef:19, true
	// 00-00-00-00-fe-80-00-00-00-00-00-00-02-00-5e-10-00-00-00-01, false
	// 0000.0000.fe80.0000.0000.0000.0200.5e10.0000.0001, false
	// 02-00-5e-10-00-00-00-01, false
	// 0200.5e10.0000.0001, false
	// 00:00:5e:00:53:01, false
	// invalid, false
	// , false
}
