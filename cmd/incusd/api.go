package main

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/mux"

	clusterConfig "github.com/lxc/incus/internal/server/cluster/config"
	clusterRequest "github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/internal/server/storage/s3"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

// swagger:operation GET / server api_get
//
//	Get the supported API endpoints
//
//	Returns a list of supported API versions (URLs).
//
//	Internal API endpoints are not reported as those aren't versioned
//	and should only be used by the daemon itself.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: ["/1.0"]
func restServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false) // Don't redirect to URL with trailing slash.
	mux.SkipClean(true)
	mux.UseEncodedPath() // Allow encoded values in path segments.

	uiPath := os.Getenv("INCUS_UI")
	uiEnabled := uiPath != "" && util.PathExists(uiPath)
	if uiEnabled {
		uiHttpDir := uiHttpDir{http.Dir(uiPath)}
		mux.PathPrefix("/ui/").Handler(http.StripPrefix("/ui/", http.FileServer(uiHttpDir)))
		mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
		})
	}

	// Serving the documentation.
	documentationPath := os.Getenv("INCUS_DOCUMENTATION")
	docEnabled := documentationPath != "" && util.PathExists(documentationPath)
	if docEnabled {
		documentationHttpDir := documentationHttpDir{http.Dir(documentationPath)}
		mux.PathPrefix("/documentation/").Handler(http.StripPrefix("/documentation/", http.FileServer(documentationHttpDir)))
		mux.HandleFunc("/documentation", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/documentation/", http.StatusMovedPermanently)
		})
	}

	// OIDC browser login (code flow).
	mux.HandleFunc("/oidc/login", func(w http.ResponseWriter, r *http.Request) {
		if d.oidcVerifier == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		d.oidcVerifier.Login(w, r)
	})

	mux.HandleFunc("/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		if d.oidcVerifier == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		d.oidcVerifier.Callback(w, r)
	})

	mux.HandleFunc("/oidc/logout", func(w http.ResponseWriter, r *http.Request) {
		if d.oidcVerifier == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		d.oidcVerifier.Logout(w, r)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		ua := r.Header.Get("User-Agent")
		if uiEnabled && strings.Contains(ua, "Gecko") {
			// Web browser handling.
			http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
		} else {
			// Normal client handling.
			_ = response.SyncResponse(true, []string{"/1.0"}).Render(w)
		}
	})

	for endpoint, f := range d.gateway.HandlerFuncs(d.heartbeatHandler, d.getTrustedCertificates) {
		mux.HandleFunc(endpoint, f)
	}

	for _, c := range api10 {
		d.createCmd(mux, "1.0", c)

		// Create any alias endpoints using the same handlers as the parent endpoint but
		// with a different path and name (so the handler can differentiate being called via
		// a different endpoint) if it wants to.
		for _, alias := range c.Aliases {
			ac := c
			ac.Name = alias.Name
			ac.Path = alias.Path
			d.createCmd(mux, "1.0", ac)
		}
	}

	for _, c := range apiInternal {
		d.createCmd(mux, "internal", c)
	}

	for _, c := range apiACME {
		d.createCmd(mux, "", c)
	}

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr})
		w.Header().Set("Content-Type", "application/json")
		_ = response.NotFound(nil).Render(w)
	})

	return &http.Server{
		Handler:     &httpServer{r: mux, d: d},
		ConnContext: request.SaveConnectionInContext,
	}
}

func hoistReqVM(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) response.Response, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		trusted, inst, err := authenticateAgentCert(d.State(), r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !trusted {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}

		resp := f(d, inst, w, r)
		_ = resp.Render(w)
	}
}

func vSockServer(d *Daemon) *http.Server {
	return &http.Server{Handler: devIncusAPI(d, hoistReqVM)}
}

func metricsServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)
	mux.SkipClean(true)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = response.SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for endpoint, f := range d.gateway.HandlerFuncs(d.heartbeatHandler, d.getTrustedCertificates) {
		mux.HandleFunc(endpoint, f)
	}

	d.createCmd(mux, "1.0", api10Cmd)
	d.createCmd(mux, "1.0", metricsCmd)

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr})
		w.Header().Set("Content-Type", "application/json")
		_ = response.NotFound(nil).Render(w)
	})

	return &http.Server{Handler: &httpServer{r: mux, d: d}}
}

func storageBucketsServer(d *Daemon) *http.Server {
	/* Setup the web server */
	m := mux.NewRouter()
	m.StrictSlash(false)
	m.SkipClean(true)

	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Wait until daemon is fully started.
		<-d.waitReady.Done()

		s := d.State()

		// Check if request contains an access key, and if so try and route it to the associated bucket.
		accessKey := s3.AuthorizationHeaderAccessKey(r.Header.Get("Authorization"))
		if accessKey != "" {
			// Lookup access key to ascertain if it maps to a bucket.
			var err error
			var bucket *db.StorageBucket
			err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				bucket, err = tx.GetStoragePoolLocalBucketByAccessKey(ctx, accessKey)
				return err
			})
			if err != nil {
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					errResult := s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID}
					errResult.Response(w)

					return
				}

				errResult := s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}
				errResult.Response(w)

				return
			}

			pool, err := storagePools.LoadByName(s, bucket.PoolName)
			if err != nil {
				errResult := s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}
				errResult.Response(w)

				return
			}

			minioProc, err := pool.ActivateBucket(bucket.Project, bucket.Name, nil)
			if err != nil {
				errResult := s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}
				errResult.Response(w)

				return
			}

			u := minioProc.URL()

			rproxy := httputil.NewSingleHostReverseProxy(&u)
			rproxy.ServeHTTP(w, r)

			return
		}

		// Otherwise treat request as anonymous.
		listResult := s3.ListAllMyBucketsResult{Owner: s3.Owner{ID: "anonymous"}}
		listResult.Response(w)
	})

	// We use the NotFoundHandler to reverse proxy requests to dynamically started local MinIO processes.
	m.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait until daemon is fully started.
		<-d.waitReady.Done()

		s := d.State()

		reqURL, err := url.Parse(r.RequestURI)
		if err != nil {
			errResult := s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}
			errResult.Response(w)

			return
		}

		pathParts := strings.Split(reqURL.Path, "/")
		if len(pathParts) < 2 {
			errResult := s3.Error{Code: s3.ErrorInvalidRequest, Message: "Bucket name not specified"}
			errResult.Response(w)

			return
		}

		bucketName, err := url.PathUnescape(pathParts[1])
		if err != nil {
			errResult := s3.Error{Code: s3.ErrorCodeNoSuchBucket, BucketName: pathParts[1]}
			errResult.Response(w)

			return
		}

		// Lookup bucket.
		var bucket *db.StorageBucket
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			bucket, err = tx.GetStoragePoolLocalBucket(ctx, bucketName)
			return err
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				errResult := s3.Error{Code: s3.ErrorCodeNoSuchBucket, BucketName: bucketName}
				errResult.Response(w)

				return
			}

			errResult := s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error(), BucketName: bucketName}
			errResult.Response(w)

			return
		}

		pool, err := storagePools.LoadByName(s, bucket.PoolName)
		if err != nil {
			errResult := s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}
			errResult.Response(w)

			return
		}

		minioProc, err := pool.ActivateBucket(bucket.Project, bucket.Name, nil)
		if err != nil {
			errResult := s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}
			errResult.Response(w)

			return
		}

		u := minioProc.URL()

		rproxy := httputil.NewSingleHostReverseProxy(&u)
		rproxy.ServeHTTP(w, r)
	})

	return &http.Server{Handler: &httpServer{r: m, d: d}}
}

type httpServer struct {
	r *mux.Router
	d *Daemon
}

func (s *httpServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.URL.Path, "/internal") {
		<-s.d.setupChan

		// Set CORS headers, unless this is an internal request.
		setCORSHeaders(rw, req, s.d.State().GlobalConfig)
	}

	// OPTIONS request don't need any further processing
	if req.Method == "OPTIONS" {
		return
	}

	// Call the original server
	s.r.ServeHTTP(rw, req)
}

func setCORSHeaders(rw http.ResponseWriter, req *http.Request, config *clusterConfig.Config) {
	allowedOrigin := config.HTTPSAllowedOrigin()
	origin := req.Header.Get("Origin")
	if allowedOrigin != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
	}

	allowedMethods := config.HTTPSAllowedMethods()
	if allowedMethods != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Methods", allowedMethods)
	}

	allowedHeaders := config.HTTPSAllowedHeaders()
	if allowedHeaders != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
	}

	allowedCredentials := config.HTTPSAllowedCredentials()
	if allowedCredentials {
		rw.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

// Return true if this an API request coming from a cluster node that is
// notifying us of some user-initiated API request that needs some action to be
// taken on this node as well.
func isClusterNotification(r *http.Request) bool {
	return r.Header.Get("User-Agent") == clusterRequest.UserAgentNotifier
}

type uiHttpDir struct {
	http.FileSystem
}

func (fs uiHttpDir) Open(name string) (http.File, error) {
	fsFile, err := fs.FileSystem.Open(name)
	if err != nil && os.IsNotExist(err) {
		return fs.FileSystem.Open("index.html")
	}

	return fsFile, err
}

type documentationHttpDir struct {
	http.FileSystem
}

func (fs documentationHttpDir) Open(name string) (http.File, error) {
	fsFile, err := fs.FileSystem.Open(name)
	if err != nil && os.IsNotExist(err) {
		return fs.FileSystem.Open("index.html")
	}

	return fsFile, err
}
