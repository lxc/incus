package cluster

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db/schema"
)

func TestOVNReferenceApplicabilityFreshAndV85Upgrade(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		name := "upgrade85-empty"
		if fresh {
			name = "fresh"
		}

		t.Run(name, func(t *testing.T) {
			database, err := sql.Open("sqlite3", ":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, database.Close()) })
			if !fresh {
				older := map[int]schema.Update{}
				for version, update := range updates {
					if version <= 85 {
						older[version] = update
					}
				}

				initial, err := schema.NewFromMap(older).Ensure(database)
				require.NoError(t, err)
				require.Zero(t, initial)
				var n int
				require.NoError(t, database.QueryRow(`SELECT count(*) FROM networks`).Scan(&n))
				require.Zero(t, n)
			}

			_, err = Schema().Ensure(database)
			require.NoError(t, err)
			var state, root string
			require.NoError(t, database.QueryRow(`SELECT state,nb_root FROM ovn_reference_applicability WHERE id=1`).Scan(&state, &root))
			if fresh {
				require.Equal(t, "never", state)
			} else {
				require.Equal(t, "unknown", state)
			}

			require.Empty(t, root)
			// Reopening the schema never changes persistent authority to fresh/never.
			_, err = Schema().Ensure(database)
			require.NoError(t, err)
			_, err = database.ExecContext(context.Background(), `UPDATE ovn_reference_applicability SET state='broken'`)
			require.Error(t, err)
			t.Logf("schema=%d initial=%s root=%q with zero current network catalog rows", SchemaVersion, state, root)
		})
	}
}
