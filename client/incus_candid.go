package incus

import (
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
)

// setupBakeryClient initializes the bakeryClient with a new client, sets its http field,
// and adds any existing interactors.
func (r *ProtocolLXD) setupBakeryClient() {
	r.bakeryClient = httpbakery.NewClient()
	r.bakeryClient.Client = r.http
	if r.bakeryInteractor != nil {
		for _, interactor := range r.bakeryInteractor {
			r.bakeryClient.AddInteractor(interactor)
		}
	}
}
