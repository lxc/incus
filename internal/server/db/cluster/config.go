//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//go:generate -command mapper generate-database db mapper -t config.mapper.go
//go:generate mapper generate -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt config objects
//generate-database:mapper stmt config create struct=Config
//generate-database:mapper stmt config delete
//
//generate-database:mapper method config GetMany
//generate-database:mapper method config Create struct=Config
//generate-database:mapper method config Update struct=Config
//generate-database:mapper method config DeleteMany

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
