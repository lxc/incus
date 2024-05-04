package storage

import (
	"context"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

type multiStorage struct {
	issuers map[string]*Storage
}

// NewMultiStorage implements the op.Storage interface by wrapping multiple storage structs
// and selecting them by the calling issuer
func NewMultiStorage(issuers []string) *multiStorage {
	s := make(map[string]*Storage)
	for _, issuer := range issuers {
		s[issuer] = NewStorage(NewUserStore(issuer))
	}
	return &multiStorage{issuers: s}
}

// CheckUsernamePassword implements the `authenticate` interface of the login
func (s *multiStorage) CheckUsernamePassword(ctx context.Context, username, password, id string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.CheckUsernamePassword(username, password, id)
}

// CreateAuthRequest implements the op.Storage interface
// it will be called after parsing and validation of the authentication request
func (s *multiStorage) CreateAuthRequest(ctx context.Context, authReq *oidc.AuthRequest, userID string) (op.AuthRequest, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.CreateAuthRequest(ctx, authReq, userID)
}

// AuthRequestByID implements the op.Storage interface
// it will be called after the Login UI redirects back to the OIDC endpoint
func (s *multiStorage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.AuthRequestByID(ctx, id)
}

// AuthRequestByCode implements the op.Storage interface
// it will be called after parsing and validation of the token request (in an authorization code flow)
func (s *multiStorage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.AuthRequestByCode(ctx, code)
}

// SaveAuthCode implements the op.Storage interface
// it will be called after the authentication has been successful and before redirecting the user agent to the redirect_uri
// (in an authorization code flow)
func (s *multiStorage) SaveAuthCode(ctx context.Context, id string, code string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.SaveAuthCode(ctx, id, code)
}

// DeleteAuthRequest implements the op.Storage interface
// it will be called after creating the token response (id and access tokens) for a valid
// - authentication request (in an implicit flow)
// - token request (in an authorization code flow)
func (s *multiStorage) DeleteAuthRequest(ctx context.Context, id string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.DeleteAuthRequest(ctx, id)
}

// CreateAccessToken implements the op.Storage interface
// it will be called for all requests able to return an access token (Authorization Code Flow, Implicit Flow, JWT Profile, ...)
func (s *multiStorage) CreateAccessToken(ctx context.Context, request op.TokenRequest) (string, time.Time, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	return storage.CreateAccessToken(ctx, request)
}

// CreateAccessAndRefreshTokens implements the op.Storage interface
// it will be called for all requests able to return an access and refresh token (Authorization Code Flow, Refresh Token Request)
func (s *multiStorage) CreateAccessAndRefreshTokens(ctx context.Context, request op.TokenRequest, currentRefreshToken string) (accessTokenID string, newRefreshToken string, expiration time.Time, err error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return storage.CreateAccessAndRefreshTokens(ctx, request, currentRefreshToken)
}

// TokenRequestByRefreshToken implements the op.Storage interface
// it will be called after parsing and validation of the refresh token request
func (s *multiStorage) TokenRequestByRefreshToken(ctx context.Context, refreshToken string) (op.RefreshTokenRequest, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.TokenRequestByRefreshToken(ctx, refreshToken)
}

// TerminateSession implements the op.Storage interface
// it will be called after the user signed out, therefore the access and refresh token of the user of this client must be removed
func (s *multiStorage) TerminateSession(ctx context.Context, userID string, clientID string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.TerminateSession(ctx, userID, clientID)
}

// GetRefreshTokenInfo looks up a refresh token and returns the token id and user id.
// If given something that is not a refresh token, it must return error.
func (s *multiStorage) GetRefreshTokenInfo(ctx context.Context, clientID string, token string) (userID string, tokenID string, err error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return "", "", err
	}
	return storage.GetRefreshTokenInfo(ctx, clientID, token)
}

// RevokeToken implements the op.Storage interface
// it will be called after parsing and validation of the token revocation request
func (s *multiStorage) RevokeToken(ctx context.Context, token string, userID string, clientID string) *oidc.Error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.RevokeToken(ctx, token, userID, clientID)
}

// SigningKey implements the op.Storage interface
// it will be called when creating the OpenID Provider
func (s *multiStorage) SigningKey(ctx context.Context) (op.SigningKey, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.SigningKey(ctx)
}

// SignatureAlgorithms implements the op.Storage interface
// it will be called to get the sign
func (s *multiStorage) SignatureAlgorithms(ctx context.Context) ([]jose.SignatureAlgorithm, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.SignatureAlgorithms(ctx)
}

// KeySet implements the op.Storage interface
// it will be called to get the current (public) keys, among others for the keys_endpoint or for validating access_tokens on the userinfo_endpoint, ...
func (s *multiStorage) KeySet(ctx context.Context) ([]op.Key, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.KeySet(ctx)
}

// GetClientByClientID implements the op.Storage interface
// it will be called whenever information (type, redirect_uris, ...) about the client behind the client_id is needed
func (s *multiStorage) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.GetClientByClientID(ctx, clientID)
}

// AuthorizeClientIDSecret implements the op.Storage interface
// it will be called for validating the client_id, client_secret on token or introspection requests
func (s *multiStorage) AuthorizeClientIDSecret(ctx context.Context, clientID, clientSecret string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.AuthorizeClientIDSecret(ctx, clientID, clientSecret)
}

// SetUserinfoFromScopes implements the op.Storage interface.
// Provide an empty implementation and use SetUserinfoFromRequest instead.
func (s *multiStorage) SetUserinfoFromScopes(ctx context.Context, userinfo *oidc.UserInfo, userID, clientID string, scopes []string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.SetUserinfoFromScopes(ctx, userinfo, userID, clientID, scopes)
}

// SetUserinfoFromRequests implements the op.CanSetUserinfoFromRequest interface.  In the
// next major release, it will be required for op.Storage.
// It will be called for the creation of an id_token, so we'll just pass it to the private function without any further check
func (s *multiStorage) SetUserinfoFromRequest(ctx context.Context, userinfo *oidc.UserInfo, token op.IDTokenRequest, scopes []string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.SetUserinfoFromRequest(ctx, userinfo, token, scopes)
}

// SetUserinfoFromToken implements the op.Storage interface
// it will be called for the userinfo endpoint, so we read the token and pass the information from that to the private function
func (s *multiStorage) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, subject, origin string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.SetUserinfoFromToken(ctx, userinfo, tokenID, subject, origin)
}

// SetIntrospectionFromToken implements the op.Storage interface
// it will be called for the introspection endpoint, so we read the token and pass the information from that to the private function
func (s *multiStorage) SetIntrospectionFromToken(ctx context.Context, introspection *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return err
	}
	return storage.SetIntrospectionFromToken(ctx, introspection, tokenID, subject, clientID)
}

// GetPrivateClaimsFromScopes implements the op.Storage interface
// it will be called for the creation of a JWT access token to assert claims for custom scopes
func (s *multiStorage) GetPrivateClaimsFromScopes(ctx context.Context, userID, clientID string, scopes []string) (claims map[string]any, err error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.GetPrivateClaimsFromScopes(ctx, userID, clientID, scopes)
}

// GetKeyByIDAndClientID implements the op.Storage interface
// it will be called to validate the signatures of a JWT (JWT Profile Grant and Authentication)
func (s *multiStorage) GetKeyByIDAndClientID(ctx context.Context, keyID, userID string) (*jose.JSONWebKey, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.GetKeyByIDAndClientID(ctx, keyID, userID)
}

// ValidateJWTProfileScopes implements the op.Storage interface
// it will be called to validate the scopes of a JWT Profile Authorization Grant request
func (s *multiStorage) ValidateJWTProfileScopes(ctx context.Context, userID string, scopes []string) ([]string, error) {
	storage, err := s.storageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return storage.ValidateJWTProfileScopes(ctx, userID, scopes)
}

// Health implements the op.Storage interface
func (s *multiStorage) Health(ctx context.Context) error {
	return nil
}

func (s *multiStorage) storageFromContext(ctx context.Context) (*Storage, *oidc.Error) {
	storage, ok := s.issuers[op.IssuerFromContext(ctx)]
	if !ok {
		return nil, oidc.ErrInvalidRequest().WithDescription("invalid issuer")
	}
	return storage, nil
}
