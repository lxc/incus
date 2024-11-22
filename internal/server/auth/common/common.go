package common

// RequestDetails is a type representing an authorization request.
type RequestDetails struct {
	Username             string
	Protocol             string
	IsAllProjectsRequest bool
	ProjectName          string
}
