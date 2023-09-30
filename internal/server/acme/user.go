package acme

import (
	"crypto"

	"github.com/go-acme/lego/v4/registration"
)

type user struct {
	Email        string
	Registration *registration.Resource
	Key          crypto.PrivateKey
}

func (a *user) GetEmail() string {
	return a.Email
}

func (a *user) GetRegistration() *registration.Resource {
	return a.Registration
}

func (a *user) GetPrivateKey() crypto.PrivateKey {
	return a.Key
}
