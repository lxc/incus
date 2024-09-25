package request

// CtxKey is the type used for all fields stored in the request context.
type CtxKey string

// Context keys.
const (
	// CtxAccess is the access field in request context.
	CtxAccess CtxKey = "access"

	// CtxConn is the connection field in the request context.
	CtxConn CtxKey = "conn"

	// CtxAddress is the address field in request context.
	CtxAddress CtxKey = "address"

	// CtxUsername is the username field in request context.
	CtxUsername CtxKey = "username"

	// CtxProtocol is the protocol field in request context.
	CtxProtocol CtxKey = "protocol"

	// CtxForwardedAddress is the forwarded address field in request context.
	CtxForwardedAddress CtxKey = "forwarded_address"

	// CtxForwardedUsername is the forwarded username field in request context.
	CtxForwardedUsername CtxKey = "forwarded_username"

	// CtxForwardedProtocol is the forwarded protocol field in request context.
	CtxForwardedProtocol CtxKey = "forwarded_protocol"
)

// Headers.
const (
	// HeaderForwardedAddress is the forwarded address field in request header.
	HeaderForwardedAddress = "X-Incus-forwarded-address"

	// HeaderForwardedUsername is the forwarded username field in request header.
	HeaderForwardedUsername = "X-Incus-forwarded-username"

	// HeaderForwardedProtocol is the forwarded protocol field in request header.
	HeaderForwardedProtocol = "X-Incus-forwarded-protocol"
)
