package main

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOVNNICMigrationOperationURLBinding(t *testing.T) {
	id := uuid.NewString()
	value, err := ovnNICMigrationOperationFromURL("https://source-member:8443/1.0/operations/" + id)
	require.NoError(t, err)
	require.Equal(t, id, value)
	for _, url := range []string{"http://source-member/1.0/operations/" + id, "https://source-member/wrong/" + id, "https://source-member/1.0/operations/not-uuid", "https://source-member/1.0/operations/" + id + "?replacement=1"} {
		_, err := ovnNICMigrationOperationFromURL(url)
		require.Error(t, err)
	}
}

func TestOVNNICMigrationPositiveHandoverRetainsCleanupError(t *testing.T) {
	sourceErr := errors.New("original OVS/BGP/host/terminal cleanup failed")
	readErr := errors.New("outcome read unavailable")
	operation := uuid.NewString()
	for _, name := range []string{"committed-clean", "committed-cleanup-error", "uncommitted-error", "uncommitted-clean", "lost-outcome", "ordinary-error"} {
		t.Run(name, func(t *testing.T) {
			selected := operation
			failure := error(nil)
			if name != "committed-clean" && name != "uncommitted-clean" {
				failure = sourceErr
			}

			if name == "ordinary-error" {
				selected = ""
			}

			reads := 0
			committed, err := ovnNICMigrationSourceWait(failure, selected, func(actual string) (bool, error) {
				reads++
				require.Equal(t, operation, actual)
				if name == "lost-outcome" {
					return false, readErr
				}

				return name == "committed-clean" || name == "committed-cleanup-error", nil
			})
			if name == "committed-clean" || name == "committed-cleanup-error" {
				require.True(t, committed)
				require.NoError(t, err)
			} else {
				require.False(t, committed)
				if name == "uncommitted-clean" {
					require.ErrorContains(t, err, "no positive durable handover")
				} else {
					require.ErrorIs(t, err, sourceErr)
				}
			}

			if name == "lost-outcome" {
				require.ErrorIs(t, err, readErr)
			}

			if name == "ordinary-error" {
				require.Zero(t, reads)
			} else {
				require.Equal(t, 1, reads)
			}
			// The original source result remains available for the API's post-placement failure.
			if name == "committed-cleanup-error" {
				require.ErrorIs(t, failure, sourceErr)
			}
		})
	}
}
