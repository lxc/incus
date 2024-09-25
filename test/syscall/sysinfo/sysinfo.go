package main

import (
	"fmt"
	"log"

	"golang.org/x/sys/unix"
)

func main() {
	var s unix.Sysinfo_t

	err := unix.Sysinfo(&s)
	if err != nil {
		log.Fatal(err)
		return
	}

	fmt.Printf("%+v\n", s)
}
