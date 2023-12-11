package endpoints

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_networkServerErrorLogWriter_shouldDiscard(t *testing.T) {
	tests := []struct {
		name    string
		proxies []net.IP
		log     []byte
		want    string
	}{
		{
			name:    "ipv4 trusted proxy (write)",
			proxies: []net.IP{net.ParseIP("10.24.0.32")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from 10.24.0.32:55672: write tcp 10.24.0.22:8443->10.24.0.32:55672: write: connection reset by peer\n"),
			want:    "",
		},
		{
			name:    "ipv4 non-trusted proxy (write)",
			proxies: []net.IP{net.ParseIP("10.24.0.33")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from 10.24.0.32:55672: write tcp 10.24.0.22:8443->10.24.0.32:55672: write: connection reset by peer\n"),
			want:    "http: TLS handshake error from 10.24.0.32:55672: write tcp 10.24.0.22:8443->10.24.0.32:55672: write: connection reset by peer",
		},
		{
			name:    "ipv6 trusted proxy (write)",
			proxies: []net.IP{net.ParseIP("2602:fd23:8:1003:216:3eff:fefa:7670")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from [2602:fd23:8:1003:216:3eff:fefa:7670]:55672: write tcp [2602:fd23:8:101::100]:8443->[2602:fd23:8:1003:216:3eff:fefa:7670]:55672: write: connection reset by peer\n"),
			want:    "",
		},
		{
			name:    "ipv6 non-trusted proxy (write)",
			proxies: []net.IP{net.ParseIP("2602:fd23:8:1003:216:3eff:fefa:7671")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from [2602:fd23:8:1003:216:3eff:fefa:7670]:55672: write tcp [2602:fd23:8:101::100]:8443->[2602:fd23:8:1003:216:3eff:fefa:7670]:55672: write: connection reset by peer\n"),
			want:    "http: TLS handshake error from [2602:fd23:8:1003:216:3eff:fefa:7670]:55672: write tcp [2602:fd23:8:101::100]:8443->[2602:fd23:8:1003:216:3eff:fefa:7670]:55672: write: connection reset by peer",
		},
		{
			name:    "ipv4 trusted proxy (read)",
			proxies: []net.IP{net.ParseIP("10.24.0.32")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from 10.24.0.32:55672: read tcp 10.24.0.22:8443->10.24.0.32:55672: read: connection reset by peer\n"),
			want:    "",
		},
		{
			name:    "ipv4 non-trusted proxy (read)",
			proxies: []net.IP{net.ParseIP("10.24.0.33")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from 10.24.0.32:55672: read tcp 10.24.0.22:8443->10.24.0.32:55672: read: connection reset by peer\n"),
			want:    "http: TLS handshake error from 10.24.0.32:55672: read tcp 10.24.0.22:8443->10.24.0.32:55672: read: connection reset by peer",
		},
		{
			name:    "ipv6 trusted proxy (read)",
			proxies: []net.IP{net.ParseIP("2602:fd23:8:1003:216:3eff:fefa:7670")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from [2602:fd23:8:1003:216:3eff:fefa:7670]:55672: read tcp [2602:fd23:8:101::100]:8443->[2602:fd23:8:1003:216:3eff:fefa:7670]:55672: read: connection reset by peer\n"),
			want:    "",
		},
		{
			name:    "ipv6 non-trusted proxy (read)",
			proxies: []net.IP{net.ParseIP("2602:fd23:8:1003:216:3eff:fefa:7671")},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: TLS handshake error from [2602:fd23:8:1003:216:3eff:fefa:7670]:55672: read tcp [2602:fd23:8:101::100]:8443->[2602:fd23:8:1003:216:3eff:fefa:7670]:55672: read: connection reset by peer\n"),
			want:    "http: TLS handshake error from [2602:fd23:8:1003:216:3eff:fefa:7670]:55672: read tcp [2602:fd23:8:101::100]:8443->[2602:fd23:8:1003:216:3eff:fefa:7670]:55672: read: connection reset by peer",
		},

		{
			name:    "unrelated",
			proxies: []net.IP{},
			log:     []byte("Sep 17 04:58:30 abydos incus.daemon[21884]: 2021/09/17 04:58:30 http: response.WriteHeader on hijacked connection from yourfunction (yourfile.go:80)\n"),
			want:    "http: response.WriteHeader on hijacked connection from yourfunction (yourfile.go:80)",
		},
	}

	for i, tt := range tests {
		t.Logf("Case %d: %s", i, tt.name)
		d := networkServerErrorLogWriter{
			proxies: tt.proxies,
		}

		assert.Equal(t, tt.want, d.stripLog(tt.log))
	}
}
