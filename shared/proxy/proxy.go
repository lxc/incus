package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

var (
	httpProxyEnv = &envOnce{
		names: []string{"HTTP_PROXY", "http_proxy"},
	}

	httpsProxyEnv = &envOnce{
		names: []string{"HTTPS_PROXY", "https_proxy"},
	}

	noProxyEnv = &envOnce{
		names: []string{"NO_PROXY", "no_proxy"},
	}
)

type envOnce struct {
	names []string
	once  sync.Once
	val   string
}

func (e *envOnce) Get() string {
	e.once.Do(e.init)
	return e.val
}

func (e *envOnce) init() {
	for _, n := range e.names {
		e.val = os.Getenv(n)
		if e.val != "" {
			return
		}
	}
}

// This is basically the same as golang's ProxyFromEnvironment, except it
// doesn't fall back to http_proxy when https_proxy isn't around, which is
// incorrect behavior. It still respects HTTP_PROXY, HTTPS_PROXY, and NO_PROXY.
func FromEnvironment(req *http.Request) (*url.URL, error) {
	return FromConfig("", "", "")(req)
}

func FromConfig(httpsProxy string, httpProxy string, noProxy string) func(req *http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		var proxy, port string
		var err error

		switch req.URL.Scheme {
		case "https":
			proxy = httpsProxy
			if proxy == "" {
				proxy = httpsProxyEnv.Get()
			}

			port = ":443"
		case "http":
			proxy = httpProxy
			if proxy == "" {
				proxy = httpProxyEnv.Get()
			}

			port = ":80"
		default:
			return nil, fmt.Errorf("unknown scheme %s", req.URL.Scheme)
		}

		if proxy == "" {
			return nil, nil
		}

		addr := req.URL.Host
		if !hasPort(addr) {
			addr = addr + port
		}

		use, err := useProxy(addr, noProxy)
		if err != nil {
			return nil, err
		}

		if !use {
			return nil, nil
		}

		proxyURL, err := url.Parse(proxy)
		if err != nil || (!strings.HasPrefix(proxyURL.Scheme, "http") && proxyURL.Scheme != "socks5") {
			// proxy was bogus. Try prepending "http://" to it and
			// see if that parses correctly. If not, we fall
			// through and complain about the original one.
			proxyURL, err := url.Parse("http://" + proxy)
			if err == nil {
				return proxyURL, nil
			}
		}
		if err != nil {
			return nil, fmt.Errorf("invalid proxy address %q: %w", proxy, err)
		}

		return proxyURL, nil
	}
}

func hasPort(s string) bool {
	return strings.LastIndex(s, ":") > strings.LastIndex(s, "]")
}

func useProxy(addr string, noProxy string) (bool, error) {
	if noProxy == "" {
		noProxy = noProxyEnv.Get()
	}

	if len(addr) == 0 {
		return true, nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, nil
	}

	if host == "localhost" {
		return false, nil
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() {
			return false, nil
		}
	}

	if noProxy == "*" {
		return false, nil
	}

	addr = strings.ToLower(strings.TrimSpace(addr))
	if hasPort(addr) {
		addr = addr[:strings.LastIndex(addr, ":")]
	}

	for _, p := range strings.Split(noProxy, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if len(p) == 0 {
			continue
		}

		if hasPort(p) {
			p = p[:strings.LastIndex(p, ":")]
		}

		if addr == p {
			return false, nil
		}

		_, pnet, err := net.ParseCIDR(p)
		if err == nil && ip != nil {
			// IPv4/CIDR, IPv6/CIDR
			if pnet.Contains(ip) {
				return false, nil
			}
		}
		if p[0] == '.' && (strings.HasSuffix(addr, p) || addr == p[1:]) {
			// noProxy ".foo.com" matches "bar.foo.com" or "foo.com"
			return false, nil
		}

		if p[0] != '.' && strings.HasSuffix(addr, p) && addr[len(addr)-len(p)-1] == '.' {
			// noProxy "foo.com" matches "bar.foo.com"
			return false, nil
		}
	}
	return true, nil
}
