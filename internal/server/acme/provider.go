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

func (p *challengeProvider) Present(domain string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.domain = domain
	p.token = token
	p.keyAuth = keyAuth

	return nil
}

func (p *challengeProvider) CleanUp(domain string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.domain = ""
	p.token = ""
	p.keyAuth = ""

	return nil
}

func (p *challengeProvider) KeyAuth() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.keyAuth
}

func (p *challengeProvider) Domain() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.domain
}

func (p *challengeProvider) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.token
}
