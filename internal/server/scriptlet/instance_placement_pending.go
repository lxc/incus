package scriptlet

import (
	"sync"

	"github.com/lxc/incus/v7/internal/server/project"
)

// pendingPlacement records an instance move that has been decided but not completed.
type pendingPlacement struct {
	project string
	name    string
	source  string
	target  string
}

var (
	pendingPlacementsMu sync.Mutex
	pendingPlacements   = map[string]pendingPlacement{}
)

// InstancePlacementPendingSet records that an instance is being moved to target.
// Until cleared, the placement scriptlet sees the instance as located on the target.
func InstancePlacementPendingSet(projectName string, instName string, source string, target string) {
	pendingPlacementsMu.Lock()
	defer pendingPlacementsMu.Unlock()

	pendingPlacements[project.Instance(projectName, instName)] = pendingPlacement{project: projectName, name: instName, source: source, target: target}
}

// InstancePlacementPendingClear forgets a pending instance move.
func InstancePlacementPendingClear(projectName string, instName string) {
	pendingPlacementsMu.Lock()
	defer pendingPlacementsMu.Unlock()

	delete(pendingPlacements, project.Instance(projectName, instName))
}

// instancePlacementPending returns the pending moves, optionally limited to a project.
func instancePlacementPending(projectName string) []pendingPlacement {
	pendingPlacementsMu.Lock()
	defer pendingPlacementsMu.Unlock()

	pending := []pendingPlacement{}
	for _, placement := range pendingPlacements {
		if projectName != "" && placement.project != projectName {
			continue
		}

		pending = append(pending, placement)
	}

	return pending
}
