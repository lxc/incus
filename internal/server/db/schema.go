//go:build linux && cgo && !agent

package db

// Directive for regenerating both the cluster and node database schemas.
//
//go:generate generate-database db schema
