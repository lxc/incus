package s3util

import (
	"net"
	"net/url"
	"strings"
)

// fallbackRegion is used for S3-compatible endpoints that don't encode an
// AWS region in their hostname (Ceph RGW, the Incus in-process handler,
// minio-style local servers, etc.). The region is part of the SigV4
// credential scope but isn't otherwise meaningful for these endpoints,
// and AWS itself accepts "us-east-1" for legacy global endpoints.
const fallbackRegion = "us-east-1"

// RegionFromURL returns the AWS region encoded in an S3 endpoint URL, or
// fallbackRegion if the URL is not an AWS S3 endpoint.
//
// Recognised AWS endpoint forms:
//
//	https://s3.amazonaws.com/...                       -> us-east-1
//	https://s3.<region>.amazonaws.com/...              -> <region>
//	https://s3-<region>.amazonaws.com/...              -> <region>  (legacy)
//	https://<bucket>.s3.amazonaws.com/...              -> us-east-1
//	https://<bucket>.s3.<region>.amazonaws.com/...     -> <region>
//	https://<bucket>.s3-<region>.amazonaws.com/...     -> <region>  (legacy)
func RegionFromURL(u *url.URL) string {
	if u == nil {
		return fallbackRegion
	}

	// Strip port if present, falling back to the raw host on error
	// (SplitHostPort errors when there is no port to split).
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
	}

	if !strings.HasSuffix(host, ".amazonaws.com") {
		return fallbackRegion
	}

	prefix := strings.TrimSuffix(host, ".amazonaws.com")
	parts := strings.Split(prefix, ".")

	// Find the s3 segment (or s3-<region> legacy form). Anything before
	// it is a virtual-hosted bucket subdomain we can ignore.
	s3Idx := -1
	for i, p := range parts {
		if p == "s3" || strings.HasPrefix(p, "s3-") {
			s3Idx = i

			break
		}
	}

	if s3Idx == -1 {
		return fallbackRegion
	}

	// Legacy s3-<region> form embeds the region in the segment itself.
	region := strings.TrimPrefix(parts[s3Idx], "s3-")
	if region != parts[s3Idx] && region != "" {
		return region
	}

	// Modern s3.<region> form: skip any qualifier segments and return
	// the next non-qualifier as the region.
	for _, p := range parts[s3Idx+1:] {
		switch p {
		case "dualstack", "fips", "accesspoint":
			continue
		}

		if p != "" {
			return p
		}
	}

	return fallbackRegion
}
