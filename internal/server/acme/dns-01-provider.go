package acme

import (
	"os"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/challenge/resolver"
	"github.com/go-acme/lego/v4/providers/dns"

	"github.com/lxc/incus/v6/shared/logger"
)

// DNS01Provider is an extension of the challenge.Provider interface.
type DNS01Provider interface {
	ChallengeProvider
}

type dns01Provider struct {
	challengeProvider
	provider challenge.Provider
	env      map[string]string
	opts     []dns01.ChallengeOption
}

// NewDNS01Provider returns a DNS01Provider.
func NewDNS01Provider(name string, env map[string]string, resolvers []string) DNS01Provider {
	for k, v := range env {
		err := os.Setenv(k, v)
		if err != nil {
			logger.Error("Failed to set environment variable", logger.Ctx{"err": err})
		}
	}

	provider, err := dns.NewDNSChallengeProviderByName(name)
	if err != nil {
		logger.Error("Failed to create DNS-01 challenge provider", logger.Ctx{"err": err})
		return nil
	}

	var opts []dns01.ChallengeOption
	if len(resolvers) > 0 {
		opts = append(opts, dns01.AddRecursiveNameservers(resolvers))
	}

	return &dns01Provider{
		env:      env,
		opts:     opts,
		provider: provider,
	}
}

// RegisterWithSolver sets the DNS-01 challenge provider for the given solver manager.
func (p *dns01Provider) RegisterWithSolver(solver *resolver.SolverManager) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return solver.SetDNS01Provider(p.provider, p.opts...)
}

// CleanUp implements the challenge.Provider interface by storing the challenge details.
func (p *dns01Provider) CleanUp(domain string, token string, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.domain = ""
	p.token = ""
	p.keyAuth = ""

	for k := range p.env {
		err := os.Unsetenv(k)
		if err != nil {
			logger.Error("Failed to unset environment variable", logger.Ctx{"err": err})
		}
	}

	return p.provider.CleanUp(domain, token, keyAuth)
}
