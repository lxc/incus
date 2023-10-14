package main

import (
	"net/http"
	"time"

	"github.com/canonical/lxd/client"

	"github.com/lxc/incus/shared/util"
)

type srcManual struct{}

func (s *srcManual) Present() bool {
	if !util.PathExists("/var/lib/lxd") {
		return false
	}

	return true
}

func (s *srcManual) Name() string {
	return "manual installation"
}

func (s *srcManual) Stop() error {
	d, err := s.Connect()
	if err != nil {
		return err
	}

	httpClient, err := d.GetHTTPClient()
	if err != nil {
		return err
	}

	// Request shutdown, this shouldn't return until daemon has stopped so use a large request timeout.
	httpTransport := httpClient.Transport.(*http.Transport)
	httpTransport.ResponseHeaderTimeout = 3600 * time.Second
	_, _, err = d.RawQuery("PUT", "/internal/shutdown", nil, "")
	if err != nil {
		return err
	}

	return nil
}

func (s *srcManual) Start() error {
	return nil
}

func (s *srcManual) Purge() error {
	return nil
}

func (s *srcManual) Connect() (lxd.InstanceServer, error) {
	return lxd.ConnectLXDUnix("/var/lib/lxd/unix.socket", nil)
}

func (s *srcManual) Paths() (*DaemonPaths, error) {
	return &DaemonPaths{
		Daemon: "/var/lib/lxd/",
		Logs:   "/var/log/lxd/",
		Cache:  "/var/cache/lxd/",
	}, nil
}
