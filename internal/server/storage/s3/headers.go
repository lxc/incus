package s3

import (
	"strings"
)

// AuthorizationHeaderAccessKey attempts to extract the (unverified) access key from the Authorization header.
func AuthorizationHeaderAccessKey(authorizationHeader string) string {
	// Parses an Authorization header as below, trying to extract the access key "PRL470D7Q93X1ZA1L82X".
	// AWS4-HMAC-SHA256 Credential=PRL470D7Q93X1ZA1L82X/20220825/US/s3/aws4_request,SignedHeaders=host;x-amz-content-sha256;x-amz-date,Signature=d8fdaf67c5072d4ff7ac56e4529e66fb08255aaa79193b212cba4670d058fade
	after, found := strings.CutPrefix(authorizationHeader, "AWS4-HMAC-SHA256")
	if found {
		authHeaderParts := strings.Split(strings.TrimSpace(after), ",")
		if strings.HasPrefix(authHeaderParts[0], "Credential=") {
			_, after, found = strings.Cut(authHeaderParts[0], "=")
			if found {
				credParts := strings.Split(after, "/")
				credPartsLen := len(credParts)
				if credPartsLen >= 5 {
					// The access key can contain / characters, so perform a reverse range search.
					return strings.Join(credParts[:credPartsLen-4], "/")
				}
			}
		}

		return ""
	}

	after, found = strings.CutPrefix(authorizationHeader, "AWS")
	if found {
		// Parses an older Authorization header as below, to extract the access key "PRL470D7Q93X1ZA1L82X".
		// AWS PRL470D7Q93X1ZA1L82X:dC5GcyRFCyQIr+y9BdpAwBjkOK0=
		authHeaderParts := strings.Split(strings.TrimSpace(after), ":")
		authHeaderPartsLen := len(authHeaderParts)
		if authHeaderPartsLen > 1 {
			return strings.Join(authHeaderParts[:authHeaderPartsLen-1], ":")
		}
	}

	return ""
}
