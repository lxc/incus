package api

import (
	"fmt"
)

func ExampleURL() {
	u := NewURL()
	fmt.Println(u.Path("1.0", "networks", "name-with-/-in-it"))
	fmt.Println(u.Project("default"))
	fmt.Println(u.Project("project-with-%-in-it"))
	fmt.Println(u.Target(""))
	fmt.Println(u.Target("member-with-%-in-it"))
	fmt.Println(u.Host("example.com"))
	fmt.Println(u.Scheme("https"))

	// Output: /1.0/networks/name-with-%2F-in-it
	// /1.0/networks/name-with-%2F-in-it
	// /1.0/networks/name-with-%2F-in-it?project=project-with-%25-in-it
	// /1.0/networks/name-with-%2F-in-it?project=project-with-%25-in-it
	// /1.0/networks/name-with-%2F-in-it?project=project-with-%25-in-it&target=member-with-%25-in-it
	// //example.com/1.0/networks/name-with-%2F-in-it?project=project-with-%25-in-it&target=member-with-%25-in-it
	// https://example.com/1.0/networks/name-with-%2F-in-it?project=project-with-%25-in-it&target=member-with-%25-in-it
}
