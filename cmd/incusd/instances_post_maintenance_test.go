//go:build linux && cgo && !agent

package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

type workloadMaintenanceBoundary struct {
	name  string
	admit func(*state.State, *http.Request, func() error) error
}

func workloadMaintenanceBoundaries() []workloadMaintenanceBoundary {
	return []workloadMaintenanceBoundary{
		{"state-unfreeze", func(s *state.State, r *http.Request, next func() error) error {
			return instanceStateMaintenanceAdmission(s, r, "unfreeze", next)
		}},
		{"image", instanceImageMaintenanceAdmission},
		{"none", instanceNoneMaintenanceAdmission},
		{"migration", instanceMigrationMaintenanceAdmission},
		{"copy", instanceCopyMaintenanceAdmission},
		{"backup", instanceBackupMaintenanceAdmission},
	}
}

func newWorkloadMaintenanceBoundaryState(t *testing.T, memberState int) *state.State {
	t.Helper()

	// Only the real isolated database is initialized; no OS or firewall facade.
	s, name := newEvacuationAdmissionState(t, memberState)
	s.ShutdownCtx = context.Background()
	s.ServerName = name
	s.ServerClustered = true
	return s
}

func workloadMaintenanceRequest(t *testing.T, protocol string, forwarded *string) *http.Request {
	t.Helper()

	r, err := http.NewRequest(http.MethodPost, "http://maintenance.test/1.0/instances", nil)
	require.NoError(t, err)
	ctx := context.WithValue(r.Context(), request.CtxProtocol, protocol)
	if forwarded != nil {
		ctx = context.WithValue(ctx, request.CtxForwardedProtocol, *forwarded)
	}

	return r.WithContext(ctx)
}

func TestWorkloadMaintenanceBoundaryMaintenance(t *testing.T) {
	for _, boundary := range workloadMaintenanceBoundaries() {
		t.Run(boundary.name, func(t *testing.T) {
			for _, member := range []struct {
				name  string
				state int
			}{
				{"evacuating", db.ClusterMemberStateEvacuating},
				{"evacuated", db.ClusterMemberStateEvacuated},
				{"restoring", db.ClusterMemberStateRestoring},
			} {
				t.Run(member.name, func(t *testing.T) {
					s := newWorkloadMaintenanceBoundaryState(t, member.state)
					r := workloadMaintenanceRequest(t, "tls", nil)
					calls := 0
					for attempt := 1; attempt <= 2; attempt++ {
						err := boundary.admit(s, r, func() error { calls++; return nil })
						require.True(t, api.StatusErrorCheck(err, http.StatusForbidden), "%v", err)
						require.EqualError(t, err, "Cluster member is evacuated")
						require.Zero(t, calls, "attempt %d must not continue", attempt)
						require.Equal(t, member.state, evacuationAdmissionState(t, s, s.ServerName))
					}
				})
			}
		})
	}
}

func TestWorkloadMaintenanceBoundaryMissingMemberAndRetry(t *testing.T) {
	for _, boundary := range workloadMaintenanceBoundaries() {
		t.Run(boundary.name, func(t *testing.T) {
			s := newWorkloadMaintenanceBoundaryState(t, db.ClusterMemberStateCreated)
			r := workloadMaintenanceRequest(t, "tls", nil)
			localID := s.DB.Cluster.GetNodeID()
			s.DB.Cluster.NodeID(999)
			calls := 0
			for attempt := 1; attempt <= 2; attempt++ {
				err := boundary.admit(s, r, func() error { calls++; return nil })
				require.Zero(t, calls, "attempt %d must reject the missing member", attempt)
				require.ErrorContains(t, err, "Failed to read local cluster member state before workload admission")
				require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
				require.Equal(t, db.ClusterMemberStateCreated, evacuationAdmissionState(t, s, s.ServerName))
			}

			s.DB.Cluster.NodeID(localID)
			require.NoError(t, boundary.admit(s, r, func() error { calls++; return nil }))
			require.Equal(t, 1, calls)
		})
	}
}

func TestWorkloadMaintenanceBoundaryQueryErrorAndRetry(t *testing.T) {
	for _, boundary := range workloadMaintenanceBoundaries() {
		t.Run(boundary.name, func(t *testing.T) {
			s := newWorkloadMaintenanceBoundaryState(t, db.ClusterMemberStateCreated)
			r := workloadMaintenanceRequest(t, "tls", nil)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			s.ShutdownCtx = ctx
			calls := 0
			for attempt := 1; attempt <= 2; attempt++ {
				err := boundary.admit(s, r, func() error { calls++; return nil })
				require.Zero(t, calls, "attempt %d must reject the failed query", attempt)
				require.ErrorContains(t, err, "Failed to read local cluster member state before workload admission")
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, db.ClusterMemberStateCreated, evacuationAdmissionState(t, s, s.ServerName))
			}

			s.ShutdownCtx = context.Background()
			require.NoError(t, boundary.admit(s, r, func() error { calls++; return nil }))
			require.Equal(t, 1, calls)
		})
	}
}

func TestWorkloadMaintenanceBoundaryActive(t *testing.T) {
	for _, boundary := range workloadMaintenanceBoundaries() {
		t.Run(boundary.name, func(t *testing.T) {
			for _, clustered := range []bool{true, false} {
				name := "created"
				if !clustered {
					name = "standalone"
				}

				t.Run(name, func(t *testing.T) {
					s := newWorkloadMaintenanceBoundaryState(t, db.ClusterMemberStateCreated)
					s.ServerClustered = clustered
					if !clustered {
						// Standalone preserves the original member-query bypass.
						s.DB.Cluster.NodeID(999)
					}

					calls := 0
					continued := errors.New("counted admitted continuation")
					err := boundary.admit(s, workloadMaintenanceRequest(t, "tls", nil), func() error {
						calls++
						return continued
					})
					require.ErrorIs(t, err, continued)
					require.Equal(t, 1, calls)
				})
			}
		})
	}
}

func TestWorkloadMaintenanceBoundaryStateProtocolAndAction(t *testing.T) {
	clusterProtocol := "cluster"
	externalProtocol := "tls"
	emptyProtocol := ""
	for _, tt := range []struct {
		name      string
		action    string
		protocol  string
		forwarded *string
		exempt    bool
	}{
		{"external-start", "start", "tls", nil, false},
		{"external-restart", "restart", "tls", nil, false},
		{"external-freeze", "freeze", "tls", nil, false},
		{"external-unfreeze", "unfreeze", "tls", nil, false},
		{"external-stop", "stop", "tls", nil, true},
		{"internal-cluster", "unfreeze", "cluster", nil, true},
		{"forwarded-external", "unfreeze", "cluster", &externalProtocol, false},
		{"forwarded-cluster", "unfreeze", "tls", &clusterProtocol, true},
		{"empty-forwarded-cluster-fallback", "start", "cluster", &emptyProtocol, true},
		{"empty-forwarded-external-fallback", "start", "tls", &emptyProtocol, false},
		{"missing-protocol", "start", "", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, fixture := range []string{"maintenance", "missing-member", "canceled-query"} {
				t.Run(fixture, func(t *testing.T) {
					s := newWorkloadMaintenanceBoundaryState(t, db.ClusterMemberStateRestoring)
					switch fixture {
					case "missing-member":
						s.DB.Cluster.NodeID(999)
					case "canceled-query":
						ctx, cancel := context.WithCancel(context.Background())
						cancel()
						s.ShutdownCtx = ctx
					}

					calls := 0
					continued := errors.New("counted exempt state continuation")
					for attempt := 1; attempt <= 2; attempt++ {
						err := instanceStateMaintenanceAdmission(s, workloadMaintenanceRequest(t, tt.protocol, tt.forwarded), tt.action, func() error {
							calls++
							return continued
						})
						if tt.exempt {
							require.ErrorIs(t, err, continued)
							require.Equal(t, attempt, calls)
						} else {
							require.Zero(t, calls)
							switch fixture {
							case "maintenance":
								require.True(t, api.StatusErrorCheck(err, http.StatusForbidden), "%v", err)
							case "missing-member":
								require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
							default:
								require.ErrorIs(t, err, context.Canceled)
							}
						}

						require.Equal(t, db.ClusterMemberStateRestoring, evacuationAdmissionState(t, s, s.ServerName))
					}
				})
			}
		})
	}
}

func TestWorkloadMaintenanceBoundaryCreationExceptions(t *testing.T) {
	clusterProtocol := "cluster"
	externalProtocol := "tls"
	for _, boundary := range workloadMaintenanceBoundaries()[1:] {
		t.Run(boundary.name, func(t *testing.T) {
			for _, tt := range []struct {
				name       string
				protocol   string
				forwarded  *string
				nilRequest bool
			}{
				{"internal-cluster", "cluster", nil, false},
				{"original-cluster-forwarded-external", "cluster", &externalProtocol, false},
				{"original-external-forwarded-cluster", "tls", &clusterProtocol, false},
				{"nil-migration", "", nil, true},
			} {
				if tt.nilRequest && boundary.name != "migration" {
					continue // Copy has no new nil-request contract.
				}

				t.Run(tt.name, func(t *testing.T) {
					for _, fixture := range []string{"maintenance", "missing-member", "canceled-query"} {
						t.Run(fixture, func(t *testing.T) {
							s := newWorkloadMaintenanceBoundaryState(t, db.ClusterMemberStateRestoring)
							switch fixture {
							case "missing-member":
								s.DB.Cluster.NodeID(999)
							case "canceled-query":
								ctx, cancel := context.WithCancel(context.Background())
								cancel()
								s.ShutdownCtx = ctx
							}

							var r *http.Request
							if !tt.nilRequest {
								r = workloadMaintenanceRequest(t, tt.protocol, tt.forwarded)
							}

							exempt := (boundary.name == "migration" && tt.nilRequest) || ((boundary.name == "migration" || boundary.name == "copy") && tt.protocol == "cluster")
							calls := 0
							continued := errors.New("counted exempt creation continuation")
							for attempt := 1; attempt <= 2; attempt++ {
								err := boundary.admit(s, r, func() error { calls++; return continued })
								if exempt {
									require.ErrorIs(t, err, continued)
									require.Equal(t, attempt, calls)
								} else {
									require.Zero(t, calls)
									switch fixture {
									case "maintenance":
										require.True(t, api.StatusErrorCheck(err, http.StatusForbidden), "%v", err)
									case "missing-member":
										require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
									default:
										require.ErrorIs(t, err, context.Canceled)
									}
								}

								require.Equal(t, db.ClusterMemberStateRestoring, evacuationAdmissionState(t, s, s.ServerName))
							}
						})
					}
				})
			}
		})
	}
}

type workloadMaintenanceUnreadReader struct{ calls int }

func (r *workloadMaintenanceUnreadReader) Read(p []byte) (int, error) {
	r.calls++
	return 0, errors.New("rejected backup must not read its upload")
}

// Never select this test with a former-decision mutation: allowed handlers have real effects.
func TestWorkloadMaintenanceRejectedCreationEntries(t *testing.T) {
	for _, fixture := range []string{"evacuating", "evacuated", "restoring", "missing-member", "canceled-query"} {
		t.Run(fixture, func(t *testing.T) {
			memberState := db.ClusterMemberStateRestoring
			switch fixture {
			case "evacuating":
				memberState = db.ClusterMemberStateEvacuating
			case "evacuated":
				memberState = db.ClusterMemberStateEvacuated
			}

			s := newWorkloadMaintenanceBoundaryState(t, memberState)
			wantCode := http.StatusForbidden
			switch fixture {
			case "missing-member":
				s.DB.Cluster.NodeID(999)
				wantCode = http.StatusNotFound
			case "canceled-query":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				s.ShutdownCtx = ctx
				wantCode = http.StatusInternalServerError
			}

			for _, entry := range []struct {
				name string
				call func(*http.Request, *workloadMaintenanceUnreadReader) response.Response
			}{
				{"image", func(r *http.Request, data *workloadMaintenanceUnreadReader) response.Response {
					return createFromImage(s, r, api.Project{}, nil, nil, "", nil)
				}},
				{"none", func(r *http.Request, data *workloadMaintenanceUnreadReader) response.Response {
					return createFromNone(s, r, "default", nil, nil)
				}},
				{"migration", func(r *http.Request, data *workloadMaintenanceUnreadReader) response.Response {
					return createFromMigration(context.Background(), s, r, "default", nil, nil)
				}},
				{"copy", func(r *http.Request, data *workloadMaintenanceUnreadReader) response.Response {
					return createFromCopy(context.Background(), s, r, "default", nil, nil)
				}},
				{"backup", func(r *http.Request, data *workloadMaintenanceUnreadReader) response.Response {
					return createFromBackup(s, r, "default", data, "", "", "", "")
				}},
			} {
				t.Run(entry.name, func(t *testing.T) {
					for attempt := 1; attempt <= 2; attempt++ {
						data := &workloadMaintenanceUnreadReader{}
						resp := entry.call(workloadMaintenanceRequest(t, "tls", nil), data)
						require.Equal(t, wantCode, resp.Code())
						if wantCode == http.StatusForbidden {
							require.Equal(t, "Cluster member is evacuated", resp.String())
						} else {
							require.Contains(t, resp.String(), "Failed to read local cluster member state before workload admission")
						}

						require.Zero(t, data.calls, "attempt %d must not read the backup upload", attempt)
						require.Equal(t, memberState, evacuationAdmissionState(t, s, s.ServerName))
					}
				})
			}
		})
	}
}
