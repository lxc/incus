package migration

import (
	"fmt"

	"github.com/lxc/incus/v6/internal/migration"
)

// IndexHeaderVersion version of the index header to be sent/recv.
const IndexHeaderVersion uint32 = 1

// ControlResponse encapsulates MigrationControl with a receive error.
type ControlResponse struct {
	migration.MigrationControl
	Err error
}

const (
	unableToLiveMigrate = "Unable to perform live container migration."
	toMigrateLive       = "To migrate the container, stop the container before migration or install CRIU"
)

var (
	ErrNoLiveMigrationSource = fmt.Errorf("%s CRIU isn't installed on the source server. %s on the source server", unableToLiveMigrate, toMigrateLive)
	ErrNoLiveMigrationTarget = fmt.Errorf("%s CRIU isn't installed on the target server. %s on the target server", unableToLiveMigrate, toMigrateLive)
	ErrNoLiveMigration       = fmt.Errorf("%s CRIU isn't installed. %s", unableToLiveMigrate, toMigrateLive)
)
