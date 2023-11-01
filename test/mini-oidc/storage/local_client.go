package storage

import (
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// IncusDeviceClient creates a device client suitable for Incus.
func IncusDeviceClient(id string) *Client {
	return &Client{
		id:              id,
		redirectURIs:    nil,
		applicationType: op.ApplicationTypeNative,
		authMethod:      oidc.AuthMethodNone,
		responseTypes:   []oidc.ResponseType{oidc.ResponseTypeCode},
		grantTypes:      []oidc.GrantType{oidc.GrantTypeDeviceCode, oidc.GrantTypeRefreshToken},
		accessTokenType: op.AccessTokenTypeJWT,
	}
}
