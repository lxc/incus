package acme

import (
	"sync"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/resolver"
)

// ChallengeProvider is an extension of the challenge.ChallengeProvider interface.
type ChallengeProvider interface {
	challenge.Provider

	Domain() string
	KeyAuth() string
	Token() string

	RegisterWithSolver(solver *resolver.SolverManager) error
}

type challengeProvider struct {
	mu      sync.Mutex
	domain  string
	keyAuth string
	token   string
}

// Present implements the challenge.Provider interface by storing the challenge details.
func (p *challengeProvider) Present(domain string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.domain = domain
	p.token = token
	p.keyAuth = keyAuth

	return nil
}

// CleanUp implements the challenge.Provider interface by clearing the challenge details.
func (p *challengeProvider) CleanUp(domain string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.domain = ""
	p.token = ""
	p.keyAuth = ""

	return nil
}

// KeyAuth returns the key authorization string for the ACME challenge.
func (p *challengeProvider) KeyAuth() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.keyAuth
}

// Domain returns the domain name for the ACME challenge.
func (p *challengeProvider) Domain() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.domain
}

// Token returns the token for the ACME challenge.
func (p *challengeProvider) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.token
}
