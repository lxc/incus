package incus

import (
	"io"

	"github.com/gorilla/websocket"
)

// The ContainerServer type is the legacy name for InstanceServer.
type ContainerServer InstanceServer

// The ContainerBackupArgs struct is used when creating a container from a backup.
type ContainerBackupArgs struct {
	// The backup file
	BackupFile io.Reader

	// Storage pool to use
	PoolName string
}

// The ContainerCopyArgs struct is used to pass additional options during container copy.
type ContainerCopyArgs struct {
	// If set, the container will be renamed on copy
	Name string

	// If set, the container running state will be transferred (live migration)
	Live bool

	// If set, only the container will copied, its snapshots won't
	ContainerOnly bool

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string

	// API extension: container_incremental_copy
	// Perform an incremental copy
	Refresh bool
}

// The ContainerSnapshotCopyArgs struct is used to pass additional options during container copy.
type ContainerSnapshotCopyArgs struct {
	// If set, the container will be renamed on copy
	Name string

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string

	// API extension: container_snapshot_stateful_migration
	// If set, the container running state will be transferred (live migration)
	Live bool
}

// The ContainerConsoleArgs struct is used to pass additional options during a
// container console session.
type ContainerConsoleArgs struct {
	// Bidirectional fd to pass to the container
	Terminal io.ReadWriteCloser

	// Control message handler (window resize)
	Control func(conn *websocket.Conn)

	// Closing this Channel causes a disconnect from the container's console
	ConsoleDisconnect chan bool
}

// The ContainerConsoleLogArgs struct is used to pass additional options during a
// container console log request.
type ContainerConsoleLogArgs struct {
}

// The ContainerExecArgs struct is used to pass additional options during container exec.
type ContainerExecArgs struct {
	// Standard input
	Stdin io.ReadCloser

	// Standard output
	Stdout io.WriteCloser

	// Standard error
	Stderr io.WriteCloser

	// Control message handler (window resize, signals, ...)
	Control func(conn *websocket.Conn)

	// Channel that will be closed when all data operations are done
	DataDone chan bool
}

// The ContainerFileArgs struct is used to pass the various options for a container file upload.
type ContainerFileArgs struct {
	// File content
	Content io.ReadSeeker

	// User id that owns the file
	UID int64

	// Group id that owns the file
	GID int64

	// File permissions
	Mode int

	// File type (file or directory)
	Type string

	// File write mode (overwrite or append)
	WriteMode string
}

// The ContainerFileResponse struct is used as part of the response for a container file download.
type ContainerFileResponse struct {
	// User id that owns the file
	UID int64

	// Group id that owns the file
	GID int64

	// File permissions
	Mode int

	// File type (file or directory)
	Type string

	// If a directory, the list of files inside it
	Entries []string
}
