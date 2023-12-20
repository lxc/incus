package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/lxc/incus/test/mini-oidc/storage"
)

func init() {
	storage.RegisterClients(
		storage.IncusDeviceClient("device"),
	)
}

func main() {
	port := os.Args[1]
	issuer := fmt.Sprintf("http://127.0.0.1:%s/", port)

	// Setup the OIDC provider.
	key := sha256.Sum256([]byte("test"))
	router := chi.NewRouter()
	users := &userStore{}
	storage := storage.NewStorage(users)

	// Create the provider.
	config := &op.Config{
		CryptoKey:               key,
		CodeMethodS256:          true,
		AuthMethodPost:          true,
		AuthMethodPrivateKeyJWT: true,
		GrantTypeRefreshToken:   true,
		RequestObjectSupported:  true,
		DeviceAuthorization: op.DeviceAuthorizationConfig{
			Lifetime:     5 * time.Minute,
			PollInterval: 5 * time.Second,
			UserFormPath: "/device",
			UserCode:     op.UserCodeBase20,
		},
	}

	provider, err := op.NewOpenIDProvider(issuer, config, storage, op.WithAllowInsecure())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Only configure device code authentication.
	router.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		userCodeHandler(storage, w, r)
	})

	// Register the root to handle discovery.
	router.Mount("/", http.Handler(provider))

	// Start listening.
	server := &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: router,
	}

	err = server.ListenAndServe()
	if err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func userCodeHandler(storage *storage.Storage, w http.ResponseWriter, r *http.Request) {
	name := username()

	err := r.ParseForm()
	if err != nil {
		return
	}

	userCode := r.Form.Get("user_code")
	if userCode == "" {
		return
	}

	err = storage.CompleteDeviceAuthorization(r.Context(), userCode, name)
	if err != nil {
		return
	}

	fmt.Printf("%s => %s\n", userCode, name)

	return
}

func username() string {
	userName := "unknown"

	content, err := os.ReadFile(os.Args[2])
	if err == nil {
		userName = strings.TrimSpace(string(content))
	}

	return userName
}

type userStore struct{}

func (u userStore) ExampleClientID() string {
	return "service"
}

func (u userStore) GetUserByID(string) *storage.User {
	name := username()

	return &storage.User{
		ID:       name,
		Username: name,
	}
}

func (u userStore) GetUserByUsername(string) *storage.User {
	name := username()

	return &storage.User{
		ID:       name,
		Username: name,
	}
}
