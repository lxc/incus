package tls

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
			"Certificate is in the first 80% of its validity period",
			args{
				domain: "foo.example.net",
				cert: &x509.Certificate{
					DNSNames:  []string{"foo.example.net"},
					NotBefore: time.Now().Add(-10 * 24 * time.Hour),
					NotAfter:  time.Now().Add(80 * 24 * time.Hour),
				},
			},
			false,
		},
		{
			"Certificate is past 80% of its validity period",
			args{
				domain: "foo.example.net",
				cert: &x509.Certificate{
					DNSNames:  []string{"foo.example.net"},
					NotBefore: time.Now().Add(-80 * 24 * time.Hour),
					NotAfter:  time.Now().Add(10 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Short-lived certificate is in the first 80% of its validity period",
			args{
				domain: "foo.example.net",
				cert: &x509.Certificate{
					DNSNames:  []string{"foo.example.net"},
					NotBefore: time.Now().Add(-4 * time.Hour),
					NotAfter:  time.Now().Add(20 * time.Hour),
				},
			},
			false,
		},
		{
			"Short-lived certificate is past 80% of its validity period",
			args{
				domain: "foo.example.net",
				cert: &x509.Certificate{
					DNSNames:  []string{"foo.example.net"},
					NotBefore: time.Now().Add(-20 * time.Hour),
					NotAfter:  time.Now().Add(4 * time.Hour),
				},
			},
			true,
		},
		{
			"Domain differs from certificate and is in the first 80% of its validity period",
			args{
				domain: "foo.example.org",
				cert: &x509.Certificate{
					DNSNames:  []string{"foo.example.net"},
					NotBefore: time.Now().Add(-10 * 24 * time.Hour),
					NotAfter:  time.Now().Add(80 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Domain differs from certificate and is past 80% of its validity period",
			args{
				domain: "foo.example.org",
				cert: &x509.Certificate{
					DNSNames:  []string{"foo.example.net"},
					NotBefore: time.Now().Add(-80 * 24 * time.Hour),
					NotAfter:  time.Now().Add(10 * 24 * time.Hour),
				},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsUpdate := CertificateNeedsUpdate(tt.args.domain, tt.args.cert)
			require.Equal(t, needsUpdate, tt.want)
		})
	}
}
