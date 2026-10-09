//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
)

func TestRetainedProfileReferenceUsageExactProjection(t *testing.T) {
	// This fixture uses real mappers and CaptureAndCommitProfileReferenceUpdate.
	f := newReferenceAccountingFixture(t)
	f.claim(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		raw, err := tx.ProfileReferenceUsage(ctx, 0, 0)
		require.NoError(t, err)
		usage, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{})
		require.NoError(t, err)
		require.Len(t, usage, len(raw))
		consumers, attempts, networkOnly := 0, 0, 0
		for i, entry := range usage {
			expected := raw[i]
			expected.NetworkProject = "default"
			expected.NetworkName = expected.Config["network"]
			require.Equal(t, expected, entry.ProfileReferenceUsage)
			require.Equal(t, "consumer", entry.InstanceName)
			require.Equal(t, "default", entry.InstanceProject)
			if entry.ConsumerID != 0 {
				consumers++
			}

			if entry.AttemptToken != "" {
				attempts++
				require.Equal(t, f.identity.Token, entry.AttemptToken)
			}

			if entry.ACLID == 0 {
				networkOnly++
				require.Empty(t, entry.ACLName)
				require.Empty(t, entry.ACLProject)
			} else {
				require.Equal(t, "old-acl", entry.ACLName)
				require.Equal(t, "default", entry.ACLProject)
			}
		}
		require.Positive(t, consumers)
		require.Positive(t, attempts)
		require.Positive(t, networkOnly)
		// Identical consumer/attempt resources remain separate, independently mutable values.
		usage[0].Config["network"] = "caller-mutated"
		for _, entry := range usage[1:] {
			require.NotEqual(t, "caller-mutated", entry.Config["network"])
		}

		again, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{})
		require.NoError(t, err)
		require.Equal(t, raw[0].Config, again[0].Config)
		for _, test := range []struct {
			name   string
			filter db.NetworkReferenceFilter
			count  int
		}{
			{"old network", db.NetworkReferenceFilter{NetworkID: f.oldNetwork}, 6},
			{"old ACL", db.NetworkReferenceFilter{ACLID: f.aclID}, 3},
			{"full identity", db.NetworkReferenceFilter{NetworkID: f.oldNetwork, NetworkProjectID: f.identity.ProjectID, ACLID: f.aclID, ACLProjectID: f.identity.ProjectID}, 3},
			{"different network", db.NetworkReferenceFilter{NetworkID: f.newNetwork, ACLID: f.aclID}, 0},
			{"wrong network project", db.NetworkReferenceFilter{NetworkID: f.oldNetwork, NetworkProjectID: f.identity.ProjectID + 1}, 0},
			{"wrong ACL project", db.NetworkReferenceFilter{ACLID: f.aclID, ACLProjectID: f.identity.ProjectID + 1}, 0},
			{"ACL project excludes network-only", db.NetworkReferenceFilter{ACLProjectID: f.identity.ProjectID}, 3},
		} {
			rows, err := tx.RetainedProfileReferenceUsage(ctx, test.filter)
			require.NoError(t, err, test.name)
			require.Len(t, rows, test.count, test.name)
		}

		for _, filter := range []db.NetworkReferenceFilter{{NetworkID: -1}, {NetworkProjectID: -1}, {ACLID: -1}, {ACLProjectID: -1}} {
			rows, err := tx.RetainedProfileReferenceUsage(ctx, filter)
			require.Error(t, err)
			require.Nil(t, rows)
		}

		return nil
	})
}

func TestRetainedProfileReferenceUsageNamesFollowRecordedIDs(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		require.NoError(t, tx.NetworkDeleting("default", "old"))
		require.NoError(t, tx.RenameNetwork(ctx, "default", "old", "renamed"))
		require.NoError(t, cluster.RenameNetworkACL(ctx, tx.Tx(), "default", "old-acl", "renamed-acl"))
		require.NoError(t, cluster.RenameInstance(ctx, tx.Tx(), "default", "consumer", "renamed-instance"))
		replacementNetwork, err := tx.CreateNetwork(ctx, "default", "old", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		replacementACL, err := cluster.CreateNetworkACL(ctx, tx.Tx(), cluster.NetworkACL{Project: "default", Name: "old-acl"})
		require.NoError(t, err)
		usage, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{NetworkID: f.oldNetwork, ACLID: f.aclID})
		require.NoError(t, err)
		require.Len(t, usage, 2)
		for _, entry := range usage {
			require.Equal(t, "renamed", entry.NetworkName)
			require.Equal(t, "renamed-acl", entry.ACLName)
			require.Equal(t, "renamed-instance", entry.InstanceName)
			require.Equal(t, "old", entry.Config["network"])
			require.Equal(t, "old-acl", entry.Config["security.acls"])
		}

		replaced, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{NetworkID: replacementNetwork, ACLID: replacementACL})
		require.NoError(t, err)
		require.Empty(t, replaced)
		for _, mutate := range []func(*db.ProfileReferenceResource){
			func(r *db.ProfileReferenceResource) { r.NetworkProjectID++ },
			func(r *db.ProfileReferenceResource) { r.ACLProjectID++ },
			func(r *db.ProfileReferenceResource) { r.NetworkID = replacementNetwork + 1000 },
			func(r *db.ProfileReferenceResource) { r.ACLID = replacementACL + 1000 },
			func(r *db.ProfileReferenceResource) { r.ACLID = 0 },
		} {
			resource := usage[0].ProfileReferenceResource
			mutate(&resource)
			resolved, err := tx.ResolveNetworkReference(ctx, resource)
			require.Error(t, err)
			require.Nil(t, resolved)
		}

		return nil
	})
}

func TestRetainedProfileReferenceUsageUnknownOutcomeKeepsProtection(t *testing.T) {
	f := newReferenceAccountingFixture(t)
	f.claim(t)
	var before []db.RetainedProfileReference
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		before, err = tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{})
		require.NoError(t, err)
		return tx.FailProfileReferenceApply(ctx, f.identity, db.ProfileReferenceUnknown)
	})
	err := f.c.Transaction(f.ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.FinalizeProfileReferenceApply(ctx, f.identity)
	})
	require.Error(t, err)
	f.tx(t, func(ctx context.Context, tx *db.ClusterTx) error {
		after, err := tx.RetainedProfileReferenceUsage(ctx, db.NetworkReferenceFilter{})
		require.NoError(t, err)
		require.Equal(t, before, after)
		return nil
	})
}
