package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/lxc/incus/v6/test/mini-oidc/minioidc"
)

func main() {
	port := os.Args[1]

	if len(os.Args) > 2 {
		minioidc.UserFile = os.Args[2]
	}

	err := minioidc.Run(port)
	if err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
