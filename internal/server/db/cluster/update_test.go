package cluster_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/db/query"
	"github.com/lxc/incus/v7/shared/osarch"
)

// These rows model stored v1 accounting only; JSON is fixture data, not application evidence.
func seedProfileReferenceStorage(t *testing.T, database *sql.DB, offset int) {
	t.Helper()
	exec := func(statement string, args ...any) {
		_, err := database.Exec(statement, args...)
		require.NoError(t, err)
	}

	projectID, networkProjectID := 100+offset, 101+offset
	nodeID, profileID, aclID := 100+offset, 100+offset, 100+offset
	exec(`INSERT INTO projects (id,name,description) VALUES (?,?,'owner fixture'), (?,?,'network fixture')`, projectID, fmt.Sprintf("owner-%d", offset), networkProjectID, fmt.Sprintf("network-owner-%d", offset))
	exec(`INSERT INTO projects_config (project_id,key,value) VALUES (?,'user.storage-test','legacy project')`, projectID)
	exec(`INSERT INTO nodes (id,name,description,address,schema,api_extensions,arch) VALUES (?,?,'',?,82,1,2)`, nodeID, fmt.Sprintf("member-%d", offset), fmt.Sprintf("fixture-%d", offset))
	exec(`INSERT INTO profiles (id,project_id,name,description,reference_generation) VALUES (?,?,'profile','unchanged profile',1)`, profileID, projectID)
	exec(`INSERT INTO profiles_config (profile_id,key,value) VALUES (?,'user.storage-test','legacy profile')`, profileID)
	exec(`INSERT INTO networks_acls (id,project_id,name,description,ingress,egress) VALUES (?,?,'acl','unchanged ACL','[]','[]')`, aclID, networkProjectID)
	change := fmt.Sprintf("change-%d", offset)
	exec(`INSERT INTO profiles_reference_changes (token,profile_id,project_id,generation,old_profile,new_profile,requested_at) VALUES (?,?,?,1,?,?,'2026-09-30 12:34:56')`, change, profileID, projectID, " {\"old\": true} ", "{\"new\":true}\n")
	for i, phase := range []string{"idle", "pending", "applying", "applied", "recovery", "retryable", "completed", "legacy"} {
		id := 100 + offset + i
		networkID, networkName := id, fmt.Sprintf("network-%d", id)
		exec(`INSERT INTO networks (id,project_id,name,description,state,type) VALUES (?,?,?,'unchanged network',1,4)`, networkID, networkProjectID, networkName)
		exec(`INSERT INTO instances (id,node_id,project_id,name,architecture,type,description) VALUES (?,?,?, ?,2,0,?)`, id, nodeID, projectID, phase, "preserved "+phase)
		exec(`INSERT INTO instances_config (instance_id,key,value) VALUES (?,'user.storage-test',?)`, id, "legacy "+phase)
		exec(`INSERT INTO instances_profiles (instance_id,profile_id,apply_order) VALUES (?,?,1)`, id, profileID)
		if phase == "legacy" {
			continue
		}

		baseline := fmt.Sprintf(" {\"Version\":1,\"fixture\":\"baseline-%d\"} \n", id)
		target := fmt.Sprintf("{\"fixture\":\"target-%d\", \"Version\":1}", id)
		desired, applied := 1, 0
		storedApplied, storedDesired, active := baseline, target, ""
		token := fmt.Sprintf("attempt-%d", id)
		if phase == "idle" {
			desired, applied, storedDesired = 0, 0, baseline
		}

		if phase == "applied" || phase == "completed" {
			applied, storedApplied = 1, target
		}

		if phase == "applying" || phase == "applied" || phase == "recovery" {
			active = token
		}

		exec(`INSERT INTO instances_profile_reference_apply (instance_id,project_id,member_id,placement_revision,desired_sequence,applied_sequence,applied_snapshot,desired_snapshot,active_token) VALUES (?,?,?,7,?,?,?,?,?)`, id, projectID, nodeID, desired, applied, storedApplied, storedDesired, active)
		if phase == "idle" {
			continue
		}

		satisfied := 0
		if phase == "completed" {
			satisfied = 1
		}

		exec(`INSERT INTO profiles_reference_consumers (id,change_token,instance_id,project_id,member_id,placement_revision,sequence,before_snapshot,after_snapshot,satisfied) VALUES (?,?,?,?,?,7,1,?,?,?)`, id, change, id, projectID, nodeID, baseline, target, satisfied)
		if phase != "completed" {
			for _, role := range []string{"before", "after"} {
				exec(`INSERT INTO profiles_reference_usage (consumer_id,instance_id,project_id,member_id,placement_revision,sequence,role,device,network_id,network_project_id,network_type,acl_id,acl_project_id,config) VALUES (?,?,?,?,7,1,?,'eth0',?,?,'ovn',?,?,?)`, id, id, projectID, nodeID, role, networkID, networkProjectID, aclID, networkProjectID, " {\"consumer\":\""+role+"\"} ")
			}
		}
		if phase == "pending" {
			continue
		}

		sealed, childSet := 0, ""
		if phase == "applied" || phase == "recovery" || phase == "completed" {
			sealed, childSet = 1, fmt.Sprintf(`[{"NetworkID":%d,"ProjectID":%d,"Name":%q,"Token":"child-%d"}]`, networkID, networkProjectID, networkName, id)
		}

		exec(`INSERT INTO profiles_reference_attempts (token,change_token,instance_id,project_id,member_id,placement_revision,sequence,owner,baseline_snapshot,target_snapshot,phase,child_set,children_sealed) VALUES (?,?,?,?,?,7,1,?,?,?,?,?,?)`, token, change, id, projectID, nodeID, fmt.Sprintf("owner-incarnation-%d", id), baseline, target, phase, childSet, sealed)
		if phase == "applied" || phase == "recovery" {
			exec(`INSERT INTO profiles_reference_children (attempt_token,network_id,project_id,name,token) VALUES (?,?,?,?,?)`, token, networkID, networkProjectID, networkName, fmt.Sprintf("child-%d", id))
			exec(`INSERT INTO networks_ovn_operations (project_id,name,node_id,token,operation,backend_fenced,ic_root) VALUES (?,?,?,?,'nic',1,'preserved-root')`, networkProjectID, networkName, nodeID, fmt.Sprintf("child-%d", id))
		}

		if phase != "completed" {
			for _, role := range []string{"baseline", "after"} {
				exec(`INSERT INTO profiles_reference_usage (attempt_token,instance_id,project_id,member_id,placement_revision,sequence,role,device,network_id,network_project_id,network_type,acl_id,acl_project_id,config) VALUES (?,?,?,?,7,1,?,'eth0',?,?,'ovn',?,?,?)`, token, id, projectID, nodeID, role, networkID, networkProjectID, aclID, networkProjectID, "{\"attempt\":\""+role+"\"}\n")
			}
		}
		if phase == "completed" {
			exec(`INSERT INTO profiles_reference_receipts (attempt_token,completed_at) VALUES (?,'2026-10-01 01:02:03.456')`, token)
		}
	}
}

func profileReferenceStorageRows(t *testing.T, database *sql.DB, statement string) [][]any {
	t.Helper()
	rows, err := database.Query(statement)
	require.NoError(t, err, statement)
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	require.NoError(t, err)
	result := [][]any{}
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}

		require.NoError(t, rows.Scan(pointers...))
		result = append(result, values)
	}

	require.NoError(t, rows.Err())
	return result
}

func profileReferenceStorageForeignKeys(t *testing.T, database *sql.DB) {
	t.Helper()
	require.Empty(t, profileReferenceStorageRows(t, database, `PRAGMA foreign_key_check`))
	var enabled int
	require.NoError(t, database.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled))
	require.Equal(t, 1, enabled)
}

func TestUpdateFromV82ProfileReferenceStorage(t *testing.T) {
	oldRows := map[string][][]any{}
	oldColumns := map[string]string{}
	oldForeignKeys := map[string][][]any{}
	var oldIndexes [][]any
	database, err := cluster.Schema().ExerciseUpdate(83, func(database *sql.DB) {
		var version int
		require.NoError(t, database.QueryRow(`SELECT max(version) FROM schema`).Scan(&version))
		require.Equal(t, 82, version)
		seedProfileReferenceStorage(t, database, 0)
		profileReferenceStorageForeignKeys(t, database)
		for _, row := range profileReferenceStorageRows(t, database, `SELECT name FROM sqlite_master WHERE type='table' AND name!='schema' ORDER BY name`) {
			table, ok := row[0].(string)
			require.True(t, ok, "table name must be a string")

			columns := []string{}
			for _, column := range profileReferenceStorageRows(t, database, `PRAGMA table_info("`+table+`")`) {
				columns = append(columns, `typeof("`+column[1].(string)+`"),quote("`+column[1].(string)+`")`)
			}

			oldColumns[table] = strings.Join(columns, ",")
			if table == "sqlite_sequence" {
				oldColumns[table] += ` FROM "sqlite_sequence" WHERE name!='schema'`
			} else {
				oldColumns[table] += ` FROM "` + table + `"`
			}

			oldRows[table] = profileReferenceStorageRows(t, database, `SELECT `+oldColumns[table]+` ORDER BY rowid`)
			oldForeignKeys[table] = profileReferenceStorageRows(t, database, `PRAGMA foreign_key_list("`+table+`")`)
		}

		oldIndexes = profileReferenceStorageRows(t, database, `SELECT name,tbl_name,sql FROM sqlite_master WHERE type='index' ORDER BY name`)
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	var version int
	require.NoError(t, database.QueryRow(`SELECT max(version) FROM schema`).Scan(&version))
	require.Equal(t, 83, version)
	for table, expected := range oldRows {
		require.Equal(t, expected, profileReferenceStorageRows(t, database, `SELECT `+oldColumns[table]+` ORDER BY rowid`), table)
		require.Equal(t, oldForeignKeys[table], profileReferenceStorageRows(t, database, `PRAGMA foreign_key_list("`+table+`")`), table)
	}

	require.Equal(t, oldIndexes, profileReferenceStorageRows(t, database, `SELECT name,tbl_name,sql FROM sqlite_master WHERE type='index' ORDER BY name`))
	profileReferenceStorageDefaults(t, database)
	// The same legacy column lists also exercise inserts made after the migration.
	seedProfileReferenceStorage(t, database, 1000)
	profileReferenceStorageDefaults(t, database)
	profileReferenceStorageConstraints(t, database)
}

func TestProfileReferenceV2StorageFreshSchema(t *testing.T) {
	database, err := sql.Open("sqlite3", ":memory:?_foreign_keys=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	_, err = cluster.Schema().Ensure(database)
	require.NoError(t, err)
	var version int
	require.NoError(t, database.QueryRow(`SELECT max(version) FROM schema`).Scan(&version))
	require.Equal(t, cluster.SchemaVersion, version)
	upgraded, err := cluster.Schema().ExerciseUpdate(cluster.SchemaVersion, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = upgraded.Close() })
	// Dump applies the production SQL formatter to both sqlite_master structures.
	upgradeDump, err := cluster.Schema().Dump(upgraded)
	require.NoError(t, err)
	freshDump, err := cluster.Schema().Dump(database)
	require.NoError(t, err)
	require.Equal(t, upgradeDump, freshDump)
	for _, table := range []string{"instances_profile_reference_apply", "profiles_reference_attempts", "profiles_reference_receipts"} {
		for _, pragma := range []string{"table_info", "foreign_key_list", "index_list"} {
			statement := `PRAGMA ` + pragma + `("` + table + `")`
			require.Equal(t, profileReferenceStorageRows(t, upgraded, statement), profileReferenceStorageRows(t, database, statement), statement)
		}
	}
	seedProfileReferenceStorage(t, database, 0)
	profileReferenceStorageDefaults(t, database)
	profileReferenceStorageConstraints(t, database)
}

func profileReferenceStorageColumns() map[string]map[string]string {
	return map[string]map[string]string{
		"instances_profile_reference_apply": {"contract_version": "1", "input_revision": "0", "materialized_observation_id": "0"},
		"profiles_reference_attempts":       {"contract_version": "1", "claimed_input_revision": "0", "generation_plan": "''", "result_snapshot": "''", "result_input_revision": "0", "observation_cutoff": "0"},
		"profiles_reference_receipts":       {"result_snapshot": "''", "post_commit_evidence": "''"},
	}
}

func profileReferenceStorageDefaults(t *testing.T, database *sql.DB) {
	t.Helper()
	for table, columns := range profileReferenceStorageColumns() {
		metadata := map[string][]any{}
		for _, row := range profileReferenceStorageRows(t, database, `PRAGMA table_info(`+table+`)`) {
			metadata[row[1].(string)] = row
		}

		for name, defaultSQL := range columns {
			row, found := metadata[name]
			require.True(t, found, "%s.%s missing", table, name)
			typeName := "INTEGER"
			var expected any = int64(0)
			switch defaultSQL {
			case "''":
				typeName, expected = "TEXT", ""
			case "1":
				expected = int64(1)
			}

			require.Equal(t, []any{typeName, int64(1), defaultSQL, int64(0)}, row[2:], table+"."+name)
			values := profileReferenceStorageRows(t, database, `SELECT `+name+` FROM `+table)
			require.NotEmpty(t, values, table)
			for _, value := range values {
				require.Equal(t, expected, value[0], table+"."+name)
			}
		}
	}

	var legacyStates int
	require.NoError(t, database.QueryRow(`SELECT count(*) FROM instances_profile_reference_apply a JOIN instances i ON i.id=a.instance_id WHERE i.name='legacy'`).Scan(&legacyStates))
	require.Zero(t, legacyStates)
	profileReferenceStorageForeignKeys(t, database)
}

func profileReferenceStorageConstraints(t *testing.T, database *sql.DB) {
	t.Helper()
	constraint := func(t *testing.T, statement string, args ...any) {
		t.Helper()
		_, err := database.Exec(statement, args...)
		var sqliteErr sqlite3.Error
		require.ErrorAs(t, err, &sqliteErr, statement)
		require.Equal(t, sqlite3.ErrConstraint, sqliteErr.Code, statement)
	}

	for table, columns := range profileReferenceStorageColumns() {
		for name, defaultSQL := range columns {
			t.Run(table+"/"+name, func(t *testing.T) {
				constraint(t, `UPDATE `+table+` SET `+name+`=NULL`)
				if defaultSQL != "''" {
					constraint(t, `UPDATE `+table+` SET `+name+`=-1`)
				}

				if name == "contract_version" {
					constraint(t, `UPDATE `+table+` SET `+name+`=0`)
					constraint(t, `UPDATE `+table+` SET `+name+`=3`)
				}

				_, err := database.Exec(`SAVEPOINT storage_domain`)
				require.NoError(t, err)
				value := any(int64(2))
				if defaultSQL == "''" {
					value = "{\"storage_only\":true}"
				}

				_, err = database.Exec(`UPDATE `+table+` SET `+name+`=?`, value)
				require.NoError(t, err)
				_, err = database.Exec(`ROLLBACK TO storage_domain; RELEASE storage_domain`)
				require.NoError(t, err)
			})
		}
	}
	t.Run("existing_relations", func(t *testing.T) {
		for _, statement := range []string{
			`INSERT INTO profiles_reference_receipts (attempt_token) VALUES ('missing')`,
			`INSERT INTO profiles_reference_receipts (attempt_token) VALUES ('attempt-106')`,
			`INSERT INTO profiles_reference_children SELECT * FROM profiles_reference_children LIMIT 1`,
			`UPDATE profiles_reference_consumers SET instance_id=101 WHERE id=102`,
			`DELETE FROM instances WHERE id=103`,
			`DELETE FROM profiles WHERE id=100`,
			`DELETE FROM profiles_reference_changes WHERE token='change-0'`,
			`DELETE FROM profiles_reference_attempts WHERE token='attempt-103'`,
			`DELETE FROM profiles_reference_attempts WHERE token='attempt-106'`,
			`DELETE FROM networks WHERE id=103`,
			`DELETE FROM networks_acls WHERE id=100`,
			`DELETE FROM projects WHERE id=100`,
		} {
			constraint(t, statement)
		}

		for column, index := range map[string]string{"network_id": "profiles_reference_usage_network", "acl_id": "profiles_reference_usage_acl"} {
			plan := profileReferenceStorageRows(t, database, `EXPLAIN QUERY PLAN SELECT * FROM profiles_reference_usage WHERE `+column+`=100`)
			require.Contains(t, fmt.Sprint(plan), index)
		}
	})
	profileReferenceStorageDefaults(t, database)
}

func TestUpdateFromV81ProfileReferenceAccounting(t *testing.T) {
	schema := cluster.Schema()
	database, err := schema.ExerciseUpdate(82, func(database *sql.DB) {
		var version int
		require.NoError(t, database.QueryRow(`SELECT max(version) FROM schema`).Scan(&version))
		require.Equal(t, 81, version)
		_, err := database.Exec(`
INSERT INTO nodes (id, name, description, address, schema, api_extensions, arch) VALUES (1, 'member', '', '1.2.3.4', 81, 1, 2);
INSERT INTO profiles (id, project_id, name, description) VALUES (100, 1, 'legacy', 'preserved');
INSERT INTO profiles_config (profile_id, key, value) VALUES (100, 'user.test', 'legacy desired');
INSERT INTO profiles_devices (id, profile_id, name, type) VALUES (100, 100, 'eth0', 1);
INSERT INTO profiles_devices_config (profile_device_id, key, value) VALUES (100, 'network', 'old');
INSERT INTO instances (id, node_id, project_id, name, architecture, type, description) VALUES (100, 1, 1, 'legacy', 2, 0, 'unknown live state');
INSERT INTO instances_profiles (instance_id, profile_id, apply_order) VALUES (100, 100, 1);
INSERT INTO networks_ovn_members (node_id, backend_id) VALUES (1, 'preserved-backend');
INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation, backend_fenced, ic_root) VALUES (1, 'old', 1, 'preserved-token', 'update', 1, 'preserved-root');
`)
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	var generation int
	var description, config, network string
	require.NoError(t, database.QueryRow(`SELECT reference_generation, description FROM profiles WHERE id=100`).Scan(&generation, &description))
	require.Zero(t, generation)
	require.Equal(t, "preserved", description)
	require.NoError(t, database.QueryRow(`SELECT value FROM profiles_config WHERE profile_id=100`).Scan(&config))
	require.Equal(t, "legacy desired", config)
	require.NoError(t, database.QueryRow(`SELECT value FROM profiles_devices_config WHERE profile_device_id=100`).Scan(&network))
	require.Equal(t, "old", network)
	for _, table := range []string{"instances_profile_reference_apply", "profiles_reference_changes", "profiles_reference_consumers", "profiles_reference_attempts", "profiles_reference_children", "profiles_reference_receipts", "profiles_reference_usage"} {
		var count int
		require.NoError(t, database.QueryRow("SELECT count(*) FROM "+table).Scan(&count))
		require.Zero(t, count, table)
	}

	var fenced int
	var token, root, backend string
	require.NoError(t, database.QueryRow(`SELECT token, backend_fenced, ic_root FROM networks_ovn_operations WHERE name='old'`).Scan(&token, &fenced, &root))
	require.Equal(t, "preserved-token", token)
	require.Equal(t, 1, fenced)
	require.Equal(t, "preserved-root", root)
	require.NoError(t, database.QueryRow(`SELECT backend_id FROM networks_ovn_members WHERE node_id=1`).Scan(&backend))
	require.Equal(t, "preserved-backend", backend)
	_, err = database.Exec(`UPDATE profiles SET reference_generation=-1 WHERE id=100`)
	require.Error(t, err)
	rows, err := database.Query(`PRAGMA foreign_key_check`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())
}

func TestUpdateFromV0(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(1, nil)
	require.NoError(t, err)

	stmt := "INSERT INTO nodes VALUES (1, 'foo', 'blah', '1.2.3.4:666', 1, 32, ?, 0)"
	_, err = db.Exec(stmt, time.Now())
	require.NoError(t, err)

	// Unique constraint on name
	stmt = "INSERT INTO nodes VALUES (2, 'foo', 'gosh', '5.6.7.8:666', 5, 20, ?, 0)"
	_, err = db.Exec(stmt, time.Now())
	require.Error(t, err)

	// Unique constraint on address
	stmt = "INSERT INTO nodes VALUES (3, 'bar', 'gasp', '1.2.3.4:666', 9, 11), ?, 0)"
	_, err = db.Exec(stmt, time.Now())
	require.Error(t, err)
}

func TestUpdateFromV1_Certificates(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO certificates VALUES (1, 'abcd:efgh', 1, 'foo', 'FOO')")
	require.NoError(t, err)

	// Unique constraint on fingerprint.
	_, err = db.Exec("INSERT INTO certificates VALUES (2, 'abcd:efgh', 2, 'bar', 'BAR')")
	require.Error(t, err)
}

func TestUpdateFromV1_Config(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO config VALUES (1, 'foo', 'blah')")
	require.NoError(t, err)

	// Unique constraint on key.
	_, err = db.Exec("INSERT INTO config VALUES (2, 'foo', 'gosh')")
	require.Error(t, err)
}

func TestUpdateFromV1_Containers(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'one', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (2, 'two', '', '2.2.2.2', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec(`
INSERT INTO containers VALUES (1, 1, 'jammy', 1, 1, 0, ?, 0, ?, 'Jammy Jellyfish')
`, time.Now(), time.Now())
	require.NoError(t, err)

	// Unique constraint on name
	_, err = db.Exec(`
INSERT INTO containers VALUES (2, 2, 'jammy', 2, 2, 1, ?, 1, ?, 'Ubuntu LTS')
`, time.Now(), time.Now())
	require.Error(t, err)

	// Cascading delete
	_, err = db.Exec("INSERT INTO containers_config VALUES (1, 1, 'thekey', 'thevalue')")
	require.NoError(t, err)
	_, err = db.Exec("DELETE FROM containers")
	require.NoError(t, err)
	result, err := db.Exec("DELETE FROM containers_config")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n) // The row was already deleted by the previous query
}

func TestUpdateFromV1_Network(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO networks VALUES (1, 'foo', 'blah', 1)")
	require.NoError(t, err)

	// Unique constraint on name.
	_, err = db.Exec("INSERT INTO networks VALUES (2, 'foo', 'gosh', 1)")
	require.Error(t, err)
}

func TestUpdateFromV1_ConfigTables(t *testing.T) {
	testConfigTable(t, "networks", func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO networks VALUES (1, 'foo', 'blah', 1)")
		require.NoError(t, err)
	})
	testConfigTable(t, "storage_pools", func(db *sql.DB) {
		_, err := db.Exec("INSERT INTO storage_pools VALUES (1, 'default', 'dir', '')")
		require.NoError(t, err)
	})
}

func testConfigTable(t *testing.T, table string, setup func(db *sql.DB)) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(2, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'one', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (2, 'two', '', '2.2.2.2', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	stmt := func(format string) string {
		return fmt.Sprintf(format, table)
	}

	setup(db)

	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (1, 1, 1, 'bar', 'baz')"))
	require.NoError(t, err)

	// Unique constraint on <entity>_id/node_id/key.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (2, 1, 1, 'bar', 'egg')"))
	require.Error(t, err)
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (3, 1, 2, 'bar', 'egg')"))
	require.NoError(t, err)

	// Reference constraint on <entity>_id.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (4, 2, 1, 'fuz', 'buz')"))
	require.Error(t, err)

	// Reference constraint on node_id.
	_, err = db.Exec(stmt("INSERT INTO %s_config VALUES (5, 1, 3, 'fuz', 'buz')"))
	require.Error(t, err)

	// Cascade deletes on node_id
	result, err := db.Exec("DELETE FROM nodes WHERE id=2")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	result, err = db.Exec(stmt("UPDATE %s_config SET value='yuk'"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n) // Only one row was affected, since the other got deleted

	// Cascade deletes on <entity>_id
	result, err = db.Exec(stmt("DELETE FROM %s"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	result, err = db.Exec(stmt("DELETE FROM %s_config"))
	require.NoError(t, err)
	n, err = result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n) // The row was already deleted by the previous query
}

func TestUpdateFromV2(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(3, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'one', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO operations VALUES (1, 'abcd', 1)")
	require.NoError(t, err)

	// Unique constraint on uuid
	_, err = db.Exec("INSERT INTO operations VALUES (2, 'abcd', 1)")
	require.Error(t, err)

	// Cascade delete on node_id
	_, err = db.Exec("DELETE FROM nodes")
	require.NoError(t, err)
	result, err := db.Exec("DELETE FROM operations")
	require.NoError(t, err)
	n, err := result.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestUpdateFromV3(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(4, nil)
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO nodes VALUES (1, 'c1', '', '1.1.1.1', 666, 999, ?, 0)", time.Now())
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO storage_pools_nodes VALUES (1, 1, 1)")
	require.NoError(t, err)

	// Unique constraint on storage_pool_id/node_id
	_, err = db.Exec("INSERT INTO storage_pools_nodes VALUES (1, 1, 1)")
	require.Error(t, err)
}

func TestUpdateFromV5(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(6, func(db *sql.DB) {
		// Create two nodes.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)
		_, err = db.Exec(
			"INSERT INTO nodes VALUES (2, 'n2', '', '5.6.7.8:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)

		// Create a pool p1 of type zfs.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a pool p2 of type ceph.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (2, 'p2', 'ceph', '', 0)")

		// Create a volume v1 on pool p1, associated with n1 and a config.
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, '')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (1, 1, 'k', 'v')")
		require.NoError(t, err)

		// Create a volume v1 on pool p2, associated with n1 and a config.
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (2, 'v1', 2, 1, 1, '')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (2, 2, 'k', 'v')")
		require.NoError(t, err)

		// Create a volume v2 on pool p2, associated with n2 and no config.
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (3, 'v2', 2, 2, 1, '')")
		require.NoError(t, err)
	})
	require.NoError(t, err)

	// Check that a volume row for n2 was added for v1 on p2.
	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	nodeIDs, err := query.SelectIntegers(context.Background(), tx, `
SELECT node_id FROM storage_volumes WHERE storage_pool_id=2 AND name='v1' ORDER BY node_id
`)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, nodeIDs)

	// Check that a volume row for n1 was added for v2 on p2.
	nodeIDs, err = query.SelectIntegers(context.Background(), tx, `
SELECT node_id FROM storage_volumes WHERE storage_pool_id=2 AND name='v2' ORDER BY node_id
`)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, nodeIDs)

	// Check that the config for volume v1 on p2 was duplicated.
	volumeIDs, err := query.SelectIntegers(context.Background(), tx, `
SELECT id FROM storage_volumes WHERE storage_pool_id=2 AND name='v1' ORDER BY id
`)
	require.NoError(t, err)
	require.Equal(t, []int{2, 4}, volumeIDs)
	config1, err := query.SelectConfig(context.Background(), tx, "storage_volumes_config", "storage_volume_id=?", volumeIDs[0])
	require.NoError(t, err)
	config2, err := query.SelectConfig(context.Background(), tx, "storage_volumes_config", "storage_volume_id=?", volumeIDs[1])
	require.NoError(t, err)
	require.Equal(t, config1, config2)
}

func TestUpdateFromV6(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(7, func(db *sql.DB) {
		// Create two nodes.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)
		_, err = db.Exec(
			"INSERT INTO nodes VALUES (2, 'n2', '', '5.6.7.8:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)

		// Create a pool p1 of type zfs.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a pool p2 of type zfs.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (2, 'p2', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a zfs.pool_name config for p1.
		_, err = db.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(1, NULL, 'zfs.pool_name', 'my-pool')
`)
		require.NoError(t, err)

		// Create a zfs.clone_copy config for p2.
		_, err = db.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(2, NULL, 'zfs.clone_copy', 'true')
`)
		require.NoError(t, err)
	})
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Check the zfs.pool_name config is now node-specific.
	for _, nodeID := range []int{1, 2} {
		config, err := query.SelectConfig(context.Background(),
			tx, "storage_pools_config", "storage_pool_id=1 AND node_id=?", nodeID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"zfs.pool_name": "my-pool"}, config)
	}

	// Check the zfs.clone_copy is still global
	config, err := query.SelectConfig(context.Background(),
		tx, "storage_pools_config", "storage_pool_id=2 AND node_id IS NULL")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"zfs.clone_copy": "true"}, config)
}

func TestUpdateFromV9(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(10, func(db *sql.DB) {
		// Create a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)

		// Create an operation.
		_, err = db.Exec("INSERT INTO operations VALUES (1, 'op1', 1)")
		require.NoError(t, err)
	})
	require.NoError(t, err)

	// Check that a type column has been added and that existing rows get type 0.
	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	types, err := query.SelectIntegers(context.Background(), tx, `SELECT type FROM operations`)
	require.NoError(t, err)
	require.Equal(t, []int{0}, types)
}

func TestUpdateFromV11(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(12, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)

		// Insert a container.
		_, err = db.Exec(`
INSERT INTO containers VALUES (1, 1, 'bionic', 1, 1, 0, ?, 0, ?, 'Bionic Beaver')
`, time.Now(), time.Now())
		require.NoError(t, err)

		// Insert an image.
		_, err = db.Exec(`
INSERT INTO images VALUES (1, 'abcd', 'img.tgz', 123, 0, 0, NULL, NULL, ?, 0, NULL, 0)
`, time.Now())
		require.NoError(t, err)

		// Insert an image alias.
		_, err = db.Exec(`
INSERT INTO images_aliases VALUES (1, 'my-img', 1, NULL)
`, time.Now())
		require.NoError(t, err)

		// Insert some profiles.
		_, err = db.Exec(`
INSERT INTO profiles VALUES (1, 'default', NULL);
INSERT INTO profiles VALUES(2, 'users', '');
INSERT INTO profiles_config VALUES(2, 2, 'boot.autostart', 'false');
INSERT INTO profiles_config VALUES(3, 2, 'limits.cpu.allowance', '50%');
INSERT INTO profiles_devices VALUES(1, 1, 'eth0', 1);
INSERT INTO profiles_devices VALUES(2, 1, 'root', 1);
INSERT INTO profiles_devices_config VALUES(1, 1, 'nictype', 'bridged');
INSERT INTO profiles_devices_config VALUES(2, 1, 'parent', 'incusbr0');
INSERT INTO profiles_devices_config VALUES(3, 2, 'path', '/');
INSERT INTO profiles_devices_config VALUES(4, 2, 'pool', 'default');
`, time.Now())
		require.NoError(t, err)
	})
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that a project_id column has been added to the various talbles
	// and that existing rows default to 1 (the ID of the default project).
	for _, table := range []string{"containers", "images", "images_aliases"} {
		count, err := query.Count(context.Background(), tx, table, "")
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		stmt := fmt.Sprintf("SELECT project_id FROM %s", table)
		ids, err := query.SelectIntegers(context.Background(), tx, stmt)
		require.NoError(t, err)
		assert.Equal(t, []int{1}, ids)
	}

	// Create a new project.
	_, err = tx.Exec(`
INSERT INTO projects VALUES (2, 'staging', 'Staging environment')`)
	require.NoError(t, err)

	// Check that it's possible to have two containers with the same name
	// as long as they are in different projects.
	_, err = tx.Exec(`
INSERT INTO containers VALUES (2, 1, 'xenial', 1, 1, 0, ?, 0, ?, 'Xenial Xerus', 1)
`, time.Now(), time.Now())
	require.NoError(t, err)

	_, err = tx.Exec(`
INSERT INTO containers VALUES (3, 1, 'xenial', 1, 1, 0, ?, 0, ?, 'Xenial Xerus', 2)
`, time.Now(), time.Now())
	require.NoError(t, err)

	// Check that it's not possible to have two containers with the same name
	// in the same project.

	_, err = tx.Exec(`
INSERT INTO containers VALUES (4, 1, 'xenial', 1, 1, 0, ?, 0, ?, 'Xenial Xerus', 1)
`, time.Now(), time.Now())
	assert.EqualError(t, err, "UNIQUE constraint failed: containers.project_id, containers.name")
}

func TestUpdateFromV14(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(15, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)

		// Insert a container.
		_, err = db.Exec(`
INSERT INTO containers VALUES (1, 1, 'eoan', 1, 1, 0, ?, 0, ?, 'Eoan Ermine', 1, NULL)
`, time.Now(), time.Now())
		require.NoError(t, err)
	})

	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that the new instances table can be queried.
	count, err := query.Count(context.Background(), tx, "instances", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestUpdateFromV15(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(16, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)

		// Insert an instance.
		_, err = db.Exec(`
INSERT INTO instances VALUES (1, 1, 'eoan', 2, 0, 0, ?, 0, ?, NULL, 1, ?)
`, time.Now(), time.Now(), time.Now())
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_config VALUES (1, 1, 'key', 'value2')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices VALUES (1, 1, 'dev', 0)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices_config VALUES (1, 1, 'k', 'v')")
		require.NoError(t, err)

		// Insert an instance snapshot.
		expiryDate := time.Date(2019, 8, 14, 11, 9, 0, 0, time.UTC)
		_, err = db.Exec(`
INSERT INTO instances VALUES (2, 1, 'eoan/snap', 2, 1, 0, ?, 0, ?, 'Eoan Ermine Snapshot', 1, ?)
`, time.Now(), time.Now(), expiryDate)
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_config VALUES (2, 2, 'key', 'value1')")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices VALUES (2, 2, 'dev', 0)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO instances_devices_config VALUES (2, 2, 'k', 'v')")
		require.NoError(t, err)
	})

	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that snapshots were migrated to the new tables.
	count, err := query.Count(context.Background(), tx, "instances", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_devices", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_devices_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots_devices", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = query.Count(context.Background(), tx, "instances_snapshots_devices_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	config, err := query.SelectConfig(context.Background(), tx, "instances_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"key": "value2"})

	config, err = query.SelectConfig(context.Background(), tx, "instances_snapshots_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"key": "value1"})

	config, err = query.SelectConfig(context.Background(), tx, "instances_devices_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"k": "v"})

	config, err = query.SelectConfig(context.Background(), tx, "instances_snapshots_devices_config", "id = 1")
	require.NoError(t, err)
	assert.Equal(t, config, map[string]string{"k": "v"})
}

func TestUpdateFromV19(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(20, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0)",
			time.Now(),
		)
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	expectedArch, err := osarch.ArchitectureGetLocalID()
	require.NoError(t, err)

	row := db.QueryRow("SELECT arch FROM nodes")
	arch := 0
	err = row.Scan(&arch)
	require.NoError(t, err)

	assert.Equal(t, expectedArch, arch)

	// Trying to create a row without specifying the architecture results
	// in an error.
	_, err = db.Exec(`
INSERT INTO nodes(id, name, description, address, schema, api_extensions, heartbeat, pending)
VALUES (2, 'n2', '', '2.2.3.4:666', 1, 32, ?, 0)`, time.Now())
	if err == nil {
		t.Fatal("expected insertion to fail")
	}

	var sqliteErr sqlite3.Error
	ok := errors.As(err, &sqliteErr)
	require.True(t, ok)
	assert.Equal(t, sqliteErr.Code, sqlite3.ErrConstraint)
}

func TestUpdateFromV25(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(26, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0, 1)",
			time.Now(),
		)
		require.NoError(t, err)

		// Insert a pool
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a volume v1 on pool p1, associated with n1 and a config.
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, '', 0, 1)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (1, 1, 'k', 'v')")
		require.NoError(t, err)

		// Create a snapshot v1/snap0 with a config.
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (2, 'v1/snap0', 1, 1, 1, '', 1, 1)")
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO storage_volumes_config VALUES (2, 2, 'k', 'v-old')")
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()

	// Check that regular volumes were kept.
	count, err := query.Count(context.Background(), tx, "storage_volumes", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	count, err = query.Count(context.Background(), tx, "storage_volumes_config", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Check that volume snapshots were migrated.
	count, err = query.Count(context.Background(), tx, "storage_volumes_snapshots", "")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	config, err := query.SelectConfig(context.Background(), tx, "storage_volumes_snapshots_config", "")
	require.NoError(t, err)
	assert.Len(t, config, 1)
	assert.Equal(t, config["k"], "v-old")
}

func TestUpdateFromV26_WithoutVolumes(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(27, func(db *sql.DB) {})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
}

func TestUpdateFromV26_WithVolumes(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(27, func(db *sql.DB) {
		// Insert a node.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0, 1)",
			time.Now(),
		)
		require.NoError(t, err)

		// Insert a pool
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'zfs', '', 0)")
		require.NoError(t, err)

		// Create a volume v1 on pool p1
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, '', 1)")
		require.NoError(t, err)

		// Create a snapshot snap0.
		_, err = db.Exec("INSERT INTO storage_volumes_snapshots VALUES (2, 1, 'snap0', '')")
		require.NoError(t, err)

		// Mess up the sqlite_sequence value.
		_, err = db.Exec("UPDATE sqlite_sequence SET seq = 1 WHERE name = 'storage_volumes'")
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	tx, err := db.Begin()
	require.NoError(t, err)

	defer func() { _ = tx.Rollback() }()
	ids, err := query.SelectIntegers(context.Background(), tx, "SELECT seq FROM sqlite_sequence WHERE name = 'storage_volumes'")
	require.NoError(t, err)

	assert.Equal(t, ids[0], 2)
}

func TestUpdateFromV34(t *testing.T) {
	schema := cluster.Schema()
	db, err := schema.ExerciseUpdate(35, func(db *sql.DB) {
		// Insert two nodes.
		_, err := db.Exec(
			"INSERT INTO nodes VALUES (1, 'n1', '', '1.2.3.4:666', 1, 32, ?, 0, 1, NULL)",
			time.Now(),
		)
		require.NoError(t, err)

		_, err = db.Exec(
			"INSERT INTO nodes VALUES (2, 'n2', '', '5.6.7.8:666', 1, 32, ?, 0, 1, NULL)",
			time.Now(),
		)
		require.NoError(t, err)

		// Insert a storage pool.
		_, err = db.Exec("INSERT INTO storage_pools VALUES (1, 'p1', 'ceph', NULL, 0)")
		require.NoError(t, err)

		// Create two rows for the same volume on different nodes.
		_, err = db.Exec("INSERT INTO storage_volumes VALUES (1, 'v1', 1, 1, 1, NULL, 1, 0)")
		require.NoError(t, err)

		_, err = db.Exec("INSERT INTO storage_volumes VALUES (2, 'v1', 1, 2, 1, NULL, 1, 0)")
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Only one volume is left and it's node ID is set to NULL.
	count, err := query.Count(context.Background(), tx, "storage_volumes", "")
	require.NoError(t, err)

	assert.Equal(t, count, 1)

	row := tx.QueryRow("SELECT id, node_id FROM storage_volumes")
	var id int
	var nodeID any
	require.NoError(t, row.Scan(&id, &nodeID))
	assert.Equal(t, id, 2)
	assert.Equal(t, nodeID, nil)
}
