package acme

import (
	"github.com/go-acme/lego/v4/challenge/resolver"
)

// HTTP01Provider is an extension of the challenge.Provider interface.
type HTTP01Provider interface {
	ChallengeProvider
}

type http01Provider struct {
	challengeProvider
}

// NewHTTP01Provider returns a HTTP01Provider.
func NewHTTP01Provider() HTTP01Provider {
	return &http01Provider{}
}

// RegisterWithSolver sets the HTTP-01 challenge provider for the given solver manager.
func (p *http01Provider) RegisterWithSolver(solver *resolver.SolverManager) error {
	return solver.SetHTTP01Provider(p)
}
