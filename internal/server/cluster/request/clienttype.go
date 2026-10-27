package request

// UserAgentNotifier used to distinguish between a regular client request and an internal cluster request when
// notifying other nodes of a cluster change.
const UserAgentNotifier = "incus-cluster-notifier"

// UserAgentClient used to distinguish between a regular client request and an internal cluster request when
// performing a regular API interaction as an internal client.
const UserAgentClient = "incus-cluster-client"

// UserAgentJoiner used to distinguish between a regular client request and an internal cluster request when
// joining a node to a cluster.
const UserAgentJoiner = "incus-cluster-joiner"

// ClientType indicates which sort of client type is being used.
type ClientType string

// ClientTypeNotifier cluster notification client.
const ClientTypeNotifier ClientType = "notifier"

// ClientTypeJoiner cluster joiner client.
const ClientTypeJoiner ClientType = "joiner"

// ClientTypeNormal normal client.
const ClientTypeNormal ClientType = "normal"

// ClientTypeInternal cluster internal client.
const ClientTypeInternal ClientType = "internal"

// UserAgentClientType converts user agent to client type.
func UserAgentClientType(userAgent string) ClientType {
	switch userAgent {
	case UserAgentNotifier:
		return ClientTypeNotifier
	case UserAgentJoiner:
		return ClientTypeJoiner
	case UserAgentClient:
		return ClientTypeInternal
	}

	return ClientTypeNormal
}
