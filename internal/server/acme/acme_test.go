package acme

import (
	"crypto/x509"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_certificateNeedsUpdate(t *testing.T) {
	type args struct {
		domain string
		cert   *x509.Certificate
	}

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"Certificate is valid for more than 30 days",
			args{
				domain: "foo.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"foo.example.net"},
					NotAfter: time.Now().Add(90 * 24 * time.Hour),
				},
			},
			false,
		},
		{
			"Certificate is valid for less than 30 days",
			args{
				domain: "foo.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"foo.example.net"},
					NotAfter: time.Now().Add(15 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Domain differs from certificate and is valid for more than 30 days",
			args{
				domain: "foo.example.org",
				cert: &x509.Certificate{
					DNSNames: []string{"foo.example.net"},
					NotAfter: time.Now().Add(90 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Domain differs from certificate and is valid for less than 30 days",
			args{
				domain: "foo.example.org",
				cert: &x509.Certificate{
					DNSNames: []string{"foo.example.net"},
					NotAfter: time.Now().Add(15 * 24 * time.Hour),
				},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsUpdate := certificateNeedsUpdate(tt.args.domain, tt.args.cert)
			require.Equal(t, needsUpdate, tt.want)
		})
	}
}
