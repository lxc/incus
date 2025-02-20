//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//generate-database:mapper target config.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e config objects
//generate-database:mapper stmt -e config create struct=Config
//generate-database:mapper stmt -e config delete
//
//generate-database:mapper method -i -e config GetMany
//generate-database:mapper method -i -e config Create struct=Config
//generate-database:mapper method -i -e config Update struct=Config
//generate-database:mapper method -i -e config DeleteMany

// Config is a reference struct representing one configuration entry of another entity.
type Config struct {
	ID          int `db:"primary=yes"`
	ReferenceID int
	Key         string
	Value       string
}

// ConfigFilter specifies potential query parameter fields.
type ConfigFilter struct {
	Key   *string
	Value *string
}
