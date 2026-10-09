package cluster_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db/cluster"
)

func TestUpdateFromV83OVNNICCleanupStorage(t *testing.T) {
	var before [][]any
	database, err := cluster.Schema().ExerciseUpdate(84, func(database *sql.DB) {
		var version int
		require.NoError(t, database.QueryRow("SELECT max(version) FROM schema").Scan(&version))
		require.Equal(t, 83, version)
		_, err := database.Exec("INSERT INTO projects (name, description) VALUES ('preserved-cleanup-project', 'preserve old rows')")
		require.NoError(t, err)
		before = profileReferenceStorageRows(t, database, `SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name`)
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	var version int
	require.NoError(t, database.QueryRow("SELECT max(version) FROM schema").Scan(&version))
	require.Equal(t, 84, version)
	var description string
	require.NoError(t, database.QueryRow("SELECT description FROM projects WHERE name='preserved-cleanup-project'").Scan(&description))
	require.Equal(t, "preserve old rows", description)
	after := profileReferenceStorageRows(t, database, `SELECT type, name, tbl_name, sql FROM sqlite_master WHERE tbl_name NOT IN ('networks_ovn_nic_cleanup', 'networks_ovn_nic_cleanup_networks', 'networks_ovn_nic_cleanup_retirement') ORDER BY type, name`)
	require.Equal(t, before, after, "NIC cleanup migration must preserve all old schema objects")

	fresh, err := sql.Open("sqlite3", ":memory:?_foreign_keys=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = fresh.Close() })
	_, err = cluster.Schema().Ensure(fresh)
	require.NoError(t, err)
	// The 83→84 assertions above stay local to that migration; compare current fresh structure after later updates.
	_, err = cluster.Schema().Ensure(database)
	require.NoError(t, err)
	oldDump, err := cluster.Schema().Dump(database)
	require.NoError(t, err)
	freshDump, err := cluster.Schema().Dump(fresh)
	require.NoError(t, err)
	require.Equal(t, oldDump, freshDump, "fresh and upgraded schema structures must match")
	rows := profileReferenceStorageRows(t, database, "SELECT count(*) FROM networks_ovn_nic_cleanup")
	require.Equal(t, [][]any{{int64(0)}}, rows, "migration must not certify any historical cleanup")
	retired := profileReferenceStorageRows(t, database, "SELECT count(*) FROM networks_ovn_nic_cleanup_retirement")
	require.Equal(t, [][]any{{int64(0)}}, retired, "migration must not retire any historical source allocations")
}
