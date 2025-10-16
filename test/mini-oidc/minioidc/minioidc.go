package minioidc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/lxc/incus/v6/test/mini-oidc/storage"
)

func init() {
	storage.RegisterClients(
		storage.IncusDeviceClient("device"),
	)
}

// UserFile is the path to a file, which contains the username to be returned
// by minioidc.
var UserFile string

// Option to configure minioidc.
type Option func(cfg *op.Config)

// WithDeviceAuthorizationPollInterval sets the device authorization poll interval.
func WithDeviceAuthorizationPollInterval(interval time.Duration) Option {
	return func(cfg *op.Config) {
		cfg.DeviceAuthorization.PollInterval = interval
	}
}

// Run starts minioidc on the given port.
// This starts ListenAndServe and will therefore block.
func Run(port string) error {
	server, err := setup(port, nil, nil)
	if err != nil {
		return err
	}

	err = server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

// RunTest runs minioidc for use in tests.
// It picks a random port and returns its address. The address
// is also the issuer URL.
func RunTest(t *testing.T, storageOpts []storage.Option, configOpts []Option) string {
	t.Helper()

	iport, err := getFreePort()
	if err != nil {
		t.Fatalf("minioidc get free port: %v", err)
	}

	port := strconv.Itoa(iport)

	server, err := setup(port, storageOpts, configOpts)
	if err != nil {
		t.Fatalf("minioidc setup: %v", err)
	}

	go func() {
		err = server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("minioidc listen and serve: %v", err)
		}
	}()

	t.Cleanup(func() {
		err := server.Shutdown(context.Background())
		if err != nil {
			t.Errorf("minioidc shutdown: %v", err)
		}
	})

	return fmt.Sprintf("http://%s/", server.Addr)
}

func setup(port string, storageOpts []storage.Option, configOpts []Option) (*http.Server, error) {
	issuer := fmt.Sprintf("http://127.0.0.1:%s/", port)

	// Setup the OIDC provider.
	key := sha256.Sum256([]byte("test"))
	router := chi.NewRouter()
	users := &userStore{}
	storageBackend := storage.NewStorage(users, storageOpts...)

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

	for _, opt := range configOpts {
		opt(config)
	}

	provider, err := op.NewProvider(config, storageBackend, op.StaticIssuer(issuer), op.WithAllowInsecure())
	if err != nil {
		return nil, err
	}

	// Only configure device code authentication.
	router.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		userCodeHandler(storageBackend, w, r)
	})

	// Register the root to handle discovery.
	router.Mount("/", http.Handler(provider))

	// Start listening.
	server := &http.Server{
		Addr:    "127.0.0.1:" + port,
		Handler: router,
	}

	return server, nil
}

func getFreePort() (port int, err error) {
	var a *net.TCPAddr
	if a, err = net.ResolveTCPAddr("tcp", "localhost:0"); err == nil {
		var l *net.TCPListener
		if l, err = net.ListenTCP("tcp", a); err == nil {
			defer l.Close() // nolint: errcheck

			return l.Addr().(*net.TCPAddr).Port, nil
		}
	}

	return port, err
}

func userCodeHandler(storageBackend *storage.Storage, w http.ResponseWriter, r *http.Request) {
	name := username()

	err := r.ParseForm()
	if err != nil {
		return
	}

	userCode := r.Form.Get("user_code")
	if userCode == "" {
		return
	}

	err = storageBackend.CompleteDeviceAuthorization(r.Context(), userCode, name)
	if err != nil {
		return
	}

	fmt.Printf("%s => %s\n", userCode, name)
}

func username() string {
	userName := "unknown"

	if UserFile != "" {
		content, err := os.ReadFile(UserFile)
		if err == nil {
			userName = strings.TrimSpace(string(content))
		}
	}

	return userName
}

type userStore struct{}

// ExampleClientID returns an example clientID.
func (u userStore) ExampleClientID() string {
	return "service"
}

// GetUserByID returns the user by ID.
func (u userStore) GetUserByID(string) *storage.User {
	name := username()

	return &storage.User{
		ID:       name,
		Username: name,
	}
}

// GetUserByUsername returns the user by username.
func (u userStore) GetUserByUsername(string) *storage.User {
	name := username()

	return &storage.User{
		ID:       name,
		Username: name,
	}
}
