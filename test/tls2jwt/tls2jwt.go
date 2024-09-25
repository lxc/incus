package main

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	if len(os.Args) < 5 {
		fmt.Printf("Usage: %s <public-certificate-file> <private-key-file> <not-before-timestamp> <duration-seconds>\n", os.Args[0])
		os.Exit(1)
	}

	keypair, err := tls.LoadX509KeyPair(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Printf("Error loading certificate: %v\n", err)
		os.Exit(1)
	}

	clientCertFingerprint := fmt.Sprintf("%x", sha256.Sum256(keypair.Certificate[0]))

	var validFromTime time.Time
	if strings.ToLower(os.Args[3]) == "now" {
		validFromTime = time.Now()
	} else {
		validFromTime, err = time.Parse(time.RFC3339, os.Args[3])
		if err != nil {
			fmt.Printf("Error parsing not-before-timestamp: %v\n", err)
			os.Exit(1)
		}
	}

	duration, err := strconv.ParseInt(os.Args[4], 10, 64)
	if err != nil {
		fmt.Printf("Error parsing duration-seconds: %v\n", err)
		os.Exit(1)
	}

	for _, alg := range []jwt.SigningMethod{jwt.SigningMethodES384, jwt.SigningMethodRS256} {
		expirationTime := validFromTime.Add(time.Second * time.Duration(duration))
		token := jwt.NewWithClaims(alg, &jwt.RegisteredClaims{
			Subject:   clientCertFingerprint,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(validFromTime),
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		})

		tokenString, err := token.SignedString(keypair.PrivateKey)
		if err != nil {
			continue
		}

		fmt.Println(tokenString)
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "Error: Couldn't sign the token (unsupported algorithm?)\n")
	os.Exit(1)
}
