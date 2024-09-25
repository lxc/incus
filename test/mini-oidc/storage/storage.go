package storage

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// serviceKey1 is a public key which will be used for the JWT Profile Authorization Grant
// the corresponding private key is in the service-key1.json (for demonstration purposes)
var serviceKey1 = &rsa.PublicKey{
	N: func() *big.Int {
		n, _ := new(big.Int).SetString("00f6d44fb5f34ac2033a75e73cb65ff24e6181edc58845e75a560ac21378284977bb055b1a75b714874e2a2641806205681c09abec76efd52cf40984edcf4c8ca09717355d11ac338f280d3e4c905b00543bdb8ee5a417496cb50cb0e29afc5a0d0471fd5a2fa625bd5281f61e6b02067d4fe7a5349eeae6d6a4300bcd86eef331", 16)
		return n
	}(),
	E: 65537,
}

var (
	_ op.Storage                  = &Storage{}
	_ op.ClientCredentialsStorage = &Storage{}
)

// storage implements the op.Storage interface
// typically you would implement this as a layer on top of your database
// for simplicity this example keeps everything in-memory
type Storage struct {
	lock          sync.Mutex
	authRequests  map[string]*AuthRequest
	codes         map[string]string
	tokens        map[string]*Token
	clients       map[string]*Client
	userStore     UserStore
	services      map[string]Service
	refreshTokens map[string]*RefreshToken
	signingKey    signingKey
	deviceCodes   map[string]deviceAuthorizationEntry
	userCodes     map[string]string
	serviceUsers  map[string]*Client
}

type signingKey struct {
	id        string
	algorithm jose.SignatureAlgorithm
	key       *rsa.PrivateKey
}

func (s *signingKey) SignatureAlgorithm() jose.SignatureAlgorithm {
	return s.algorithm
}

func (s *signingKey) Key() any {
	return s.key
}

func (s *signingKey) ID() string {
	return s.id
}

type publicKey struct {
	signingKey
}

func (s *publicKey) ID() string {
	return s.id
}

func (s *publicKey) Algorithm() jose.SignatureAlgorithm {
	return s.algorithm
}

func (s *publicKey) Use() string {
	return "sig"
}

func (s *publicKey) Key() any {
	return &s.key.PublicKey
}

func NewStorage(userStore UserStore) *Storage {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	return &Storage{
		authRequests:  make(map[string]*AuthRequest),
		codes:         make(map[string]string),
		tokens:        make(map[string]*Token),
		refreshTokens: make(map[string]*RefreshToken),
		clients:       clients,
		userStore:     userStore,
		services: map[string]Service{
			userStore.ExampleClientID(): {
				keys: map[string]*rsa.PublicKey{
					"key1": serviceKey1,
				},
			},
		},
		signingKey: signingKey{
			id:        uuid.NewString(),
			algorithm: jose.RS256,
			key:       key,
		},
		deviceCodes: make(map[string]deviceAuthorizationEntry),
		userCodes:   make(map[string]string),
		serviceUsers: map[string]*Client{
			"sid1": {
				id:     "sid1",
				secret: "verysecret",
				grantTypes: []oidc.GrantType{
					oidc.GrantTypeClientCredentials,
				},
				accessTokenType: op.AccessTokenTypeBearer,
			},
		},
	}
}

// CheckUsernamePassword implements the `authenticate` interface of the login
func (s *Storage) CheckUsernamePassword(username, password, id string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	request, ok := s.authRequests[id]
	if !ok {
		return fmt.Errorf("request not found")
	}

	// for demonstration purposes we'll check we'll have a simple user store and
	// a plain text password.  For real world scenarios, be sure to have the password
	// hashed and salted (e.g. using bcrypt)
	user := s.userStore.GetUserByUsername(username)
	if user != nil && user.Password == password {
		// be sure to set user id into the auth request after the user was checked,
		// so that you'll be able to get more information about the user after the login
		request.UserID = user.ID

		// you will have to change some state on the request to guide the user through possible multiple steps of the login process
		// in this example we'll simply check the username / password and set a boolean to true
		// therefore we will also just check this boolean if the request / login has been finished
		request.done = true
		return nil
	}
	return fmt.Errorf("username or password wrong")
}

func (s *Storage) CheckUsernamePasswordSimple(username, password string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	user := s.userStore.GetUserByUsername(username)
	if user != nil && user.Password == password {
		return nil
	}
	return fmt.Errorf("username or password wrong")
}

// CreateAuthRequest implements the op.Storage interface
// it will be called after parsing and validation of the authentication request
func (s *Storage) CreateAuthRequest(ctx context.Context, authReq *oidc.AuthRequest, userID string) (op.AuthRequest, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if len(authReq.Prompt) == 1 && authReq.Prompt[0] == "none" {
		// With prompt=none, there is no way for the user to log in
		// so return error right away.
		return nil, oidc.ErrLoginRequired()
	}

	// typically, you'll fill your storage / storage model with the information of the passed object
	request := authRequestToInternal(authReq, userID)

	// you'll also have to create a unique id for the request (this might be done by your database; we'll use a uuid)
	request.ID = uuid.NewString()

	// and save it in your database (for demonstration purposed we will use a simple map)
	s.authRequests[request.ID] = request

	// finally, return the request (which implements the AuthRequest interface of the OP
	return request, nil
}

// AuthRequestByID implements the op.Storage interface
// it will be called after the Login UI redirects back to the OIDC endpoint
func (s *Storage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	request, ok := s.authRequests[id]
	if !ok {
		return nil, fmt.Errorf("request not found")
	}
	return request, nil
}

// AuthRequestByCode implements the op.Storage interface
// it will be called after parsing and validation of the token request (in an authorization code flow)
func (s *Storage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	// for this example we read the id by code and then get the request by id
	requestID, ok := func() (string, bool) {
		s.lock.Lock()
		defer s.lock.Unlock()
		requestID, ok := s.codes[code]
		return requestID, ok
	}()
	if !ok {
		return nil, fmt.Errorf("code invalid or expired")
	}
	return s.AuthRequestByID(ctx, requestID)
}

// SaveAuthCode implements the op.Storage interface
// it will be called after the authentication has been successful and before redirecting the user agent to the redirect_uri
// (in an authorization code flow)
func (s *Storage) SaveAuthCode(ctx context.Context, id string, code string) error {
	// for this example we'll just save the authRequestID to the code
	s.lock.Lock()
	defer s.lock.Unlock()
	s.codes[code] = id
	return nil
}

// DeleteAuthRequest implements the op.Storage interface
// it will be called after creating the token response (id and access tokens) for a valid
// - authentication request (in an implicit flow)
// - token request (in an authorization code flow)
func (s *Storage) DeleteAuthRequest(ctx context.Context, id string) error {
	// you can simply delete all reference to the auth request
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.authRequests, id)
	for code, requestID := range s.codes {
		if id == requestID {
			delete(s.codes, code)
			return nil
		}
	}
	return nil
}

// CreateAccessToken implements the op.Storage interface
// it will be called for all requests able to return an access token (Authorization Code Flow, Implicit Flow, JWT Profile, ...)
func (s *Storage) CreateAccessToken(ctx context.Context, request op.TokenRequest) (string, time.Time, error) {
	var applicationID string
	switch req := request.(type) {
	case *AuthRequest:
		// if authenticated for an app (auth code / implicit flow) we must save the client_id to the token
		applicationID = req.ApplicationID
	case op.TokenExchangeRequest:
		applicationID = req.GetClientID()
	}

	token, err := s.accessToken(applicationID, "", request.GetSubject(), request.GetAudience(), request.GetScopes())
	if err != nil {
		return "", time.Time{}, err
	}
	return token.ID, token.Expiration, nil
}

// CreateAccessAndRefreshTokens implements the op.Storage interface
// it will be called for all requests able to return an access and refresh token (Authorization Code Flow, Refresh Token Request)
func (s *Storage) CreateAccessAndRefreshTokens(ctx context.Context, request op.TokenRequest, currentRefreshToken string) (accessTokenID string, newRefreshToken string, expiration time.Time, err error) {
	// generate tokens via token exchange flow if request is relevant
	if teReq, ok := request.(op.TokenExchangeRequest); ok {
		return s.exchangeRefreshToken(ctx, teReq)
	}

	// get the information depending on the request type / implementation
	applicationID, authTime, amr := getInfoFromRequest(request)

	// if currentRefreshToken is empty (Code Flow) we will have to create a new refresh token
	if currentRefreshToken == "" {
		refreshTokenID := uuid.NewString()
		accessToken, err := s.accessToken(applicationID, refreshTokenID, request.GetSubject(), request.GetAudience(), request.GetScopes())
		if err != nil {
			return "", "", time.Time{}, err
		}
		refreshToken, err := s.createRefreshToken(accessToken, amr, authTime)
		if err != nil {
			return "", "", time.Time{}, err
		}
		return accessToken.ID, refreshToken, accessToken.Expiration, nil
	}

	// if we get here, the currentRefreshToken was not empty, so the call is a refresh token request
	// we therefore will have to check the currentRefreshToken and renew the refresh token
	refreshToken, refreshTokenID, err := s.renewRefreshToken(currentRefreshToken)
	if err != nil {
		return "", "", time.Time{}, err
	}
	accessToken, err := s.accessToken(applicationID, refreshTokenID, request.GetSubject(), request.GetAudience(), request.GetScopes())
	if err != nil {
		return "", "", time.Time{}, err
	}
	return accessToken.ID, refreshToken, accessToken.Expiration, nil
}

func (s *Storage) exchangeRefreshToken(ctx context.Context, request op.TokenExchangeRequest) (accessTokenID string, newRefreshToken string, expiration time.Time, err error) {
	applicationID := request.GetClientID()
	authTime := request.GetAuthTime()

	refreshTokenID := uuid.NewString()
	accessToken, err := s.accessToken(applicationID, refreshTokenID, request.GetSubject(), request.GetAudience(), request.GetScopes())
	if err != nil {
		return "", "", time.Time{}, err
	}

	refreshToken, err := s.createRefreshToken(accessToken, nil, authTime)
	if err != nil {
		return "", "", time.Time{}, err
	}

	return accessToken.ID, refreshToken, accessToken.Expiration, nil
}

// TokenRequestByRefreshToken implements the op.Storage interface
// it will be called after parsing and validation of the refresh token request
func (s *Storage) TokenRequestByRefreshToken(ctx context.Context, refreshToken string) (op.RefreshTokenRequest, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	token, ok := s.refreshTokens[refreshToken]
	if !ok {
		return nil, fmt.Errorf("invalid refresh_token")
	}
	return RefreshTokenRequestFromBusiness(token), nil
}

// TerminateSession implements the op.Storage interface
// it will be called after the user signed out, therefore the access and refresh token of the user of this client must be removed
func (s *Storage) TerminateSession(ctx context.Context, userID string, clientID string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, token := range s.tokens {
		if token.ApplicationID == clientID && token.Subject == userID {
			delete(s.tokens, token.ID)
			delete(s.refreshTokens, token.RefreshTokenID)
		}
	}
	return nil
}

// GetRefreshTokenInfo looks up a refresh token and returns the token id and user id.
// If given something that is not a refresh token, it must return error.
func (s *Storage) GetRefreshTokenInfo(ctx context.Context, clientID string, token string) (userID string, tokenID string, err error) {
	refreshToken, ok := s.refreshTokens[token]
	if !ok {
		return "", "", op.ErrInvalidRefreshToken
	}
	return refreshToken.UserID, refreshToken.ID, nil
}

// RevokeToken implements the op.Storage interface
// it will be called after parsing and validation of the token revocation request
func (s *Storage) RevokeToken(ctx context.Context, tokenIDOrToken string, userID string, clientID string) *oidc.Error {
	// a single token was requested to be removed
	s.lock.Lock()
	defer s.lock.Unlock()
	accessToken, ok := s.tokens[tokenIDOrToken] // tokenID
	if ok {
		if accessToken.ApplicationID != clientID {
			return oidc.ErrInvalidClient().WithDescription("token was not issued for this client")
		}
		// if it is an access token, just remove it
		// you could also remove the corresponding refresh token if really necessary
		delete(s.tokens, accessToken.ID)
		return nil
	}
	refreshToken, ok := s.refreshTokens[tokenIDOrToken] // token
	if !ok {
		// if the token is neither an access nor a refresh token, just ignore it, the expected behaviour of
		// being not valid (anymore) is achieved
		return nil
	}
	if refreshToken.ApplicationID != clientID {
		return oidc.ErrInvalidClient().WithDescription("token was not issued for this client")
	}
	// if it is a refresh token, you will have to remove the access token as well
	delete(s.refreshTokens, refreshToken.ID)
	for _, accessToken := range s.tokens {
		if accessToken.RefreshTokenID == refreshToken.ID {
			delete(s.tokens, accessToken.ID)
			return nil
		}
	}
	return nil
}

// SigningKey implements the op.Storage interface
// it will be called when creating the OpenID Provider
func (s *Storage) SigningKey(ctx context.Context) (op.SigningKey, error) {
	// in this example the signing key is a static rsa.PrivateKey and the algorithm used is RS256
	// you would obviously have a more complex implementation and store / retrieve the key from your database as well
	return &s.signingKey, nil
}

// SignatureAlgorithms implements the op.Storage interface
// it will be called to get the sign
func (s *Storage) SignatureAlgorithms(context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{s.signingKey.algorithm}, nil
}

// KeySet implements the op.Storage interface
// it will be called to get the current (public) keys, among others for the keys_endpoint or for validating access_tokens on the userinfo_endpoint, ...
func (s *Storage) KeySet(ctx context.Context) ([]op.Key, error) {
	// as mentioned above, this example only has a single signing key without key rotation,
	// so it will directly use its public key
	//
	// when using key rotation you typically would store the public keys alongside the private keys in your database
	// and give both of them an expiration date, with the public key having a longer lifetime
	return []op.Key{&publicKey{s.signingKey}}, nil
}

// GetClientByClientID implements the op.Storage interface
// it will be called whenever information (type, redirect_uris, ...) about the client behind the client_id is needed
func (s *Storage) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	client, ok := s.clients[clientID]
	if !ok {
		return nil, fmt.Errorf("client not found")
	}
	return RedirectGlobsClient(client), nil
}

// AuthorizeClientIDSecret implements the op.Storage interface
// it will be called for validating the client_id, client_secret on token or introspection requests
func (s *Storage) AuthorizeClientIDSecret(ctx context.Context, clientID, clientSecret string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	client, ok := s.clients[clientID]
	if !ok {
		return fmt.Errorf("client not found")
	}
	// for this example we directly check the secret
	// obviously you would not have the secret in plain text, but rather hashed and salted (e.g. using bcrypt)
	if client.secret != clientSecret {
		return fmt.Errorf("invalid secret")
	}
	return nil
}

// SetUserinfoFromScopes implements the op.Storage interface.
// Provide an empty implementation and use SetUserinfoFromRequest instead.
func (s *Storage) SetUserinfoFromScopes(ctx context.Context, userinfo *oidc.UserInfo, userID, clientID string, scopes []string) error {
	return nil
}

// SetUserinfoFromRequests implements the op.CanSetUserinfoFromRequest interface.  In the
// next major release, it will be required for op.Storage.
// It will be called for the creation of an id_token, so we'll just pass it to the private function without any further check
func (s *Storage) SetUserinfoFromRequest(ctx context.Context, userinfo *oidc.UserInfo, token op.IDTokenRequest, scopes []string) error {
	return s.setUserinfo(ctx, userinfo, token.GetSubject(), token.GetClientID(), scopes)
}

// SetUserinfoFromToken implements the op.Storage interface
// it will be called for the userinfo endpoint, so we read the token and pass the information from that to the private function
func (s *Storage) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, subject, origin string) error {
	token, ok := func() (*Token, bool) {
		s.lock.Lock()
		defer s.lock.Unlock()
		token, ok := s.tokens[tokenID]
		return token, ok
	}()
	if !ok {
		return fmt.Errorf("token is invalid or has expired")
	}
	// the userinfo endpoint should support CORS. If it's not possible to specify a specific origin in the CORS handler,
	// and you have to specify a wildcard (*) origin, then you could also check here if the origin which called the userinfo endpoint here directly
	// note that the origin can be empty (if called by a web client)
	//
	// if origin != "" {
	//	client, ok := s.clients[token.ApplicationID]
	//	if !ok {
	//		return fmt.Errorf("client not found")
	//	}
	//	if err := checkAllowedOrigins(client.allowedOrigins, origin); err != nil {
	//		return err
	//	}
	//}
	return s.setUserinfo(ctx, userinfo, token.Subject, token.ApplicationID, token.Scopes)
}

// SetIntrospectionFromToken implements the op.Storage interface
// it will be called for the introspection endpoint, so we read the token and pass the information from that to the private function
func (s *Storage) SetIntrospectionFromToken(ctx context.Context, introspection *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	token, ok := func() (*Token, bool) {
		s.lock.Lock()
		defer s.lock.Unlock()
		token, ok := s.tokens[tokenID]
		return token, ok
	}()
	if !ok {
		return fmt.Errorf("token is invalid or has expired")
	}
	// check if the client is part of the requested audience
	for _, aud := range token.Audience {
		if aud == clientID {
			// the introspection response only has to return a boolean (active) if the token is active
			// this will automatically be done by the library if you don't return an error
			// you can also return further information about the user / associated token
			// e.g. the userinfo (equivalent to userinfo endpoint)

			userInfo := new(oidc.UserInfo)
			err := s.setUserinfo(ctx, userInfo, subject, clientID, token.Scopes)
			if err != nil {
				return err
			}
			introspection.SetUserInfo(userInfo)
			//...and also the requested scopes...
			introspection.Scope = token.Scopes
			//...and the client the token was issued to
			introspection.ClientID = token.ApplicationID
			return nil
		}
	}
	return fmt.Errorf("token is not valid for this client")
}

// GetPrivateClaimsFromScopes implements the op.Storage interface
// it will be called for the creation of a JWT access token to assert claims for custom scopes
func (s *Storage) GetPrivateClaimsFromScopes(ctx context.Context, userID, clientID string, scopes []string) (claims map[string]any, err error) {
	return s.getPrivateClaimsFromScopes(ctx, userID, clientID, scopes)
}

func (s *Storage) getPrivateClaimsFromScopes(ctx context.Context, userID, clientID string, scopes []string) (claims map[string]any, err error) {
	for _, scope := range scopes {
		switch scope {
		case CustomScope:
			claims = appendClaim(claims, CustomClaim, customClaim(clientID))
		}
	}
	return claims, nil
}

// GetKeyByIDAndClientID implements the op.Storage interface
// it will be called to validate the signatures of a JWT (JWT Profile Grant and Authentication)
func (s *Storage) GetKeyByIDAndClientID(ctx context.Context, keyID, clientID string) (*jose.JSONWebKey, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	service, ok := s.services[clientID]
	if !ok {
		return nil, fmt.Errorf("clientID not found")
	}
	key, ok := service.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("key not found")
	}
	return &jose.JSONWebKey{
		KeyID: keyID,
		Use:   "sig",
		Key:   key,
	}, nil
}

// ValidateJWTProfileScopes implements the op.Storage interface
// it will be called to validate the scopes of a JWT Profile Authorization Grant request
func (s *Storage) ValidateJWTProfileScopes(ctx context.Context, userID string, scopes []string) ([]string, error) {
	allowedScopes := make([]string, 0)
	for _, scope := range scopes {
		if scope == oidc.ScopeOpenID {
			allowedScopes = append(allowedScopes, scope)
		}
	}
	return allowedScopes, nil
}

// Health implements the op.Storage interface
func (s *Storage) Health(ctx context.Context) error {
	return nil
}

// createRefreshToken will store a refresh_token in-memory based on the provided information
func (s *Storage) createRefreshToken(accessToken *Token, amr []string, authTime time.Time) (string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	token := &RefreshToken{
		ID:            accessToken.RefreshTokenID,
		Token:         accessToken.RefreshTokenID,
		AuthTime:      authTime,
		AMR:           amr,
		ApplicationID: accessToken.ApplicationID,
		UserID:        accessToken.Subject,
		Audience:      accessToken.Audience,
		Expiration:    time.Now().Add(5 * time.Hour),
		Scopes:        accessToken.Scopes,
	}
	s.refreshTokens[token.ID] = token
	return token.Token, nil
}

// renewRefreshToken checks the provided refresh_token and creates a new one based on the current
func (s *Storage) renewRefreshToken(currentRefreshToken string) (string, string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	refreshToken, ok := s.refreshTokens[currentRefreshToken]
	if !ok {
		return "", "", fmt.Errorf("invalid refresh token")
	}
	// deletes the refresh token and all access tokens which were issued based on this refresh token
	delete(s.refreshTokens, currentRefreshToken)
	for _, token := range s.tokens {
		if token.RefreshTokenID == currentRefreshToken {
			delete(s.tokens, token.ID)
			break
		}
	}
	// creates a new refresh token based on the current one
	token := uuid.NewString()
	refreshToken.Token = token
	refreshToken.ID = token
	s.refreshTokens[token] = refreshToken
	return token, refreshToken.ID, nil
}

// accessToken will store an access_token in-memory based on the provided information
func (s *Storage) accessToken(applicationID, refreshTokenID, subject string, audience, scopes []string) (*Token, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	token := &Token{
		ID:             uuid.NewString(),
		ApplicationID:  applicationID,
		RefreshTokenID: refreshTokenID,
		Subject:        subject,
		Audience:       audience,
		Expiration:     time.Now().Add(5 * time.Minute),
		Scopes:         scopes,
	}
	s.tokens[token.ID] = token
	return token, nil
}

// setUserinfo sets the info based on the user, scopes and if necessary the clientID
func (s *Storage) setUserinfo(ctx context.Context, userInfo *oidc.UserInfo, userID, clientID string, scopes []string) (err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	user := s.userStore.GetUserByID(userID)
	if user == nil {
		return fmt.Errorf("user not found")
	}
	for _, scope := range scopes {
		switch scope {
		case oidc.ScopeOpenID:
			userInfo.Subject = user.ID
		case oidc.ScopeEmail:
			userInfo.Email = user.Email
			userInfo.EmailVerified = oidc.Bool(user.EmailVerified)
		case oidc.ScopeProfile:
			userInfo.PreferredUsername = user.Username
			userInfo.Name = user.FirstName + " " + user.LastName
			userInfo.FamilyName = user.LastName
			userInfo.GivenName = user.FirstName
			userInfo.Locale = oidc.NewLocale(user.PreferredLanguage)
		case oidc.ScopePhone:
			userInfo.PhoneNumber = user.Phone
			userInfo.PhoneNumberVerified = user.PhoneVerified
		case CustomScope:
			// you can also have a custom scope and assert public or custom claims based on that
			userInfo.AppendClaims(CustomClaim, customClaim(clientID))
		}
	}
	return nil
}

// ValidateTokenExchangeRequest implements the op.TokenExchangeStorage interface
// it will be called to validate parsed Token Exchange Grant request
func (s *Storage) ValidateTokenExchangeRequest(ctx context.Context, request op.TokenExchangeRequest) error {
	if request.GetRequestedTokenType() == "" {
		request.SetRequestedTokenType(oidc.RefreshTokenType)
	}

	// Just an example, some use cases might need this use case
	if request.GetExchangeSubjectTokenType() == oidc.IDTokenType && request.GetRequestedTokenType() == oidc.RefreshTokenType {
		return errors.New("exchanging id_token to refresh_token is not supported")
	}

	// Check impersonation permissions
	if request.GetExchangeActor() == "" && !s.userStore.GetUserByID(request.GetExchangeSubject()).IsAdmin {
		return errors.New("user doesn't have impersonation permission")
	}

	allowedScopes := make([]string, 0)
	for _, scope := range request.GetScopes() {
		if scope == oidc.ScopeAddress {
			continue
		}

		if strings.HasPrefix(scope, CustomScopeImpersonatePrefix) {
			subject := strings.TrimPrefix(scope, CustomScopeImpersonatePrefix)
			request.SetSubject(subject)
		}

		allowedScopes = append(allowedScopes, scope)
	}

	request.SetCurrentScopes(allowedScopes)

	return nil
}

// ValidateTokenExchangeRequest implements the op.TokenExchangeStorage interface
// Common use case is to store request for audit purposes. For this example we skip the storing.
func (s *Storage) CreateTokenExchangeRequest(ctx context.Context, request op.TokenExchangeRequest) error {
	return nil
}

// GetPrivateClaimsFromScopesForTokenExchange implements the op.TokenExchangeStorage interface
// it will be called for the creation of an exchanged JWT access token to assert claims for custom scopes
// plus adding token exchange specific claims related to delegation or impersonation
func (s *Storage) GetPrivateClaimsFromTokenExchangeRequest(ctx context.Context, request op.TokenExchangeRequest) (claims map[string]any, err error) {
	claims, err = s.getPrivateClaimsFromScopes(ctx, "", request.GetClientID(), request.GetScopes())
	if err != nil {
		return nil, err
	}

	for k, v := range s.getTokenExchangeClaims(ctx, request) {
		claims = appendClaim(claims, k, v)
	}

	return claims, nil
}

// SetUserinfoFromScopesForTokenExchange implements the op.TokenExchangeStorage interface
// it will be called for the creation of an id_token - we are using the same private function as for other flows,
// plus adding token exchange specific claims related to delegation or impersonation
func (s *Storage) SetUserinfoFromTokenExchangeRequest(ctx context.Context, userinfo *oidc.UserInfo, request op.TokenExchangeRequest) error {
	err := s.setUserinfo(ctx, userinfo, request.GetSubject(), request.GetClientID(), request.GetScopes())
	if err != nil {
		return err
	}

	for k, v := range s.getTokenExchangeClaims(ctx, request) {
		userinfo.AppendClaims(k, v)
	}

	return nil
}

func (s *Storage) getTokenExchangeClaims(ctx context.Context, request op.TokenExchangeRequest) (claims map[string]any) {
	for _, scope := range request.GetScopes() {
		switch {
		case strings.HasPrefix(scope, CustomScopeImpersonatePrefix) && request.GetExchangeActor() == "":
			// Set actor subject claim for impersonation flow
			claims = appendClaim(claims, "act", map[string]any{
				"sub": request.GetExchangeSubject(),
			})
		}
	}

	// Set actor subject claim for delegation flow
	// if request.GetExchangeActor() != "" {
	// 	claims = appendClaim(claims, "act", map[string]any{
	// 		"sub": request.GetExchangeActor(),
	// 	})
	// }

	return claims
}

// getInfoFromRequest returns the clientID, authTime and amr depending on the op.TokenRequest type / implementation
func getInfoFromRequest(req op.TokenRequest) (clientID string, authTime time.Time, amr []string) {
	authReq, ok := req.(*AuthRequest) // Code Flow (with scope offline_access)
	if ok {
		return authReq.ApplicationID, authReq.authTime, authReq.GetAMR()
	}
	refreshReq, ok := req.(*RefreshTokenRequest) // Refresh Token Request
	if ok {
		return refreshReq.ApplicationID, refreshReq.AuthTime, refreshReq.AMR
	}
	return "", time.Time{}, nil
}

// customClaim demonstrates how to return custom claims based on provided information
func customClaim(clientID string) map[string]any {
	return map[string]any{
		"client": clientID,
		"other":  "stuff",
	}
}

func appendClaim(claims map[string]any, claim string, value any) map[string]any {
	if claims == nil {
		claims = make(map[string]any)
	}
	claims[claim] = value
	return claims
}

type deviceAuthorizationEntry struct {
	deviceCode string
	userCode   string
	state      *op.DeviceAuthorizationState
}

func (s *Storage) StoreDeviceAuthorization(ctx context.Context, clientID, deviceCode, userCode string, expires time.Time, scopes []string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.clients[clientID]; !ok {
		return errors.New("client not found")
	}

	if _, ok := s.userCodes[userCode]; ok {
		return op.ErrDuplicateUserCode
	}

	s.deviceCodes[deviceCode] = deviceAuthorizationEntry{
		deviceCode: deviceCode,
		userCode:   userCode,
		state: &op.DeviceAuthorizationState{
			ClientID: clientID,
			Scopes:   scopes,
			Expires:  expires,
		},
	}

	s.userCodes[userCode] = deviceCode
	return nil
}

func (s *Storage) GetDeviceAuthorizatonState(ctx context.Context, clientID, deviceCode string) (*op.DeviceAuthorizationState, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	entry, ok := s.deviceCodes[deviceCode]
	if !ok || entry.state.ClientID != clientID {
		return nil, errors.New("device code not found for client") // is there a standard not found error in the framework?
	}

	return entry.state, nil
}

func (s *Storage) GetDeviceAuthorizationByUserCode(ctx context.Context, userCode string) (*op.DeviceAuthorizationState, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	entry, ok := s.deviceCodes[s.userCodes[userCode]]
	if !ok {
		return nil, errors.New("user code not found")
	}

	return entry.state, nil
}

func (s *Storage) CompleteDeviceAuthorization(ctx context.Context, userCode, subject string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	entry, ok := s.deviceCodes[s.userCodes[userCode]]
	if !ok {
		return errors.New("user code not found")
	}

	entry.state.Subject = subject
	entry.state.Done = true
	return nil
}

func (s *Storage) DenyDeviceAuthorization(ctx context.Context, userCode string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.deviceCodes[s.userCodes[userCode]].state.Denied = true
	return nil
}

// AuthRequestDone is used by testing and is not required to implement op.Storage
func (s *Storage) AuthRequestDone(id string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if req, ok := s.authRequests[id]; ok {
		req.done = true
		return nil
	}

	return errors.New("request not found")
}

func (s *Storage) ClientCredentials(ctx context.Context, clientID, clientSecret string) (op.Client, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	client, ok := s.serviceUsers[clientID]
	if !ok {
		return nil, errors.New("wrong service user or password")
	}
	if client.secret != clientSecret {
		return nil, errors.New("wrong service user or password")
	}

	return client, nil
}

func (s *Storage) ClientCredentialsTokenRequest(ctx context.Context, clientID string, scopes []string) (op.TokenRequest, error) {
	client, ok := s.serviceUsers[clientID]
	if !ok {
		return nil, errors.New("wrong service user or password")
	}

	return &oidc.JWTTokenRequest{
		Subject:  client.id,
		Audience: []string{clientID},
		Scopes:   scopes,
	}, nil
}
