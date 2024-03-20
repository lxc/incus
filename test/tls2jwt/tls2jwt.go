package main

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func loadPrivateKey(fileName string) (*rsa.PrivateKey, error) {
	k, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(k)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("Failed to decode PEM block containing the private key")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	return key.(*rsa.PrivateKey), nil
}

func loadCertificate(fileName string) (*x509.Certificate, error) {
	crt, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(crt)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("Failed to decode PEM block containing the certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	return cert, nil
}

func certFingerprint(cert *x509.Certificate) string {
	return fmt.Sprintf("%x", sha256.Sum256(cert.Raw))
}

func main() {
	if len(os.Args) < 5 {
		fmt.Printf("Usage: %s <private-key-file> <public-certificate-file> <not-before-timestamp> <duration-seconds>\n", os.Args[0])
		os.Exit(1)
	}

	privateKey, err := loadPrivateKey(os.Args[1])
	if err != nil {
		fmt.Printf("Error loading private key: %v\n", err)
		os.Exit(1)
	}

	clientCert, err := loadCertificate(os.Args[2])
	if err != nil {
		fmt.Printf("Error loading client certificate: %v\n", err)
		os.Exit(1)
	}

	clientCertFingerprint := certFingerprint(clientCert)

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

	expirationTime := validFromTime.Add(time.Second * time.Duration(duration))
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, &jwt.RegisteredClaims{
		Subject:   clientCertFingerprint,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		NotBefore: jwt.NewNumericDate(validFromTime),
		ExpiresAt: jwt.NewNumericDate(expirationTime),
	})

	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		fmt.Printf("Error signing the token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s", tokenString)
	os.Exit(0)
}
