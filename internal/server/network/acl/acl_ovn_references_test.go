package acl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lxc/incus/v7/shared/api"
)

func TestOVNReferencedACLClassifier(t *testing.T) {
	info := &api.NetworkACL{NetworkACLPut: api.NetworkACLPut{
		Ingress: []api.NetworkACLRule{
			{Source: " office, $office, alpha, @network/peer, @internal, @external, #internal, #external, 192.0.2.1, 2001:db8::1, 192.0.2.0/24, 2001:db8::/64, 192.0.2.1-192.0.2.8, 2001:db8::1-2001:db8::8, alpha, , ", Destination: "ingress-opposite"},
			{Source: "disabled-ingress", State: "disabled"},
			{Source: ""},
		},
		Egress: []api.NetworkACLRule{
			{Destination: " beta, office, $set, @other/peer, @, @malformed, $, $$malformed, 198.51.100.1, ::1, 0.0.0.0/0, ::/0, 198.51.100.1-198.51.100.2, ::1-::2, ,", Source: "egress-opposite"},
			{Destination: "disabled-egress", State: "disabled"},
			{Destination: " , "},
		},
	}}
	got := map[string]struct{}{"preexisting": {}}
	ovnAddReferencedACLs(info, got)
	want := map[string]struct{}{"preexisting": {}, "office": {}, "alpha": {}, "beta": {}, "disabled-ingress": {}, "disabled-egress": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bare ACL names = %v; want %v", got, want)
	}
}

func TestOVNReferencedACLClassifierEmpty(t *testing.T) {
	got := make(map[string]struct{})
	ovnAddReferencedACLs(&api.NetworkACL{}, got)
	if len(got) != 0 {
		t.Fatalf("empty rules contributed %v", got)
	}
}

func referenceRules(ingress string, egress string) *api.NetworkACL {
	return &api.NetworkACL{NetworkACLPut: api.NetworkACLPut{
		Ingress: []api.NetworkACLRule{{Source: ingress}},
		Egress:  []api.NetworkACLRule{{Destination: egress}},
	}}
}

func TestOVNReferencedACLPlannerLoadedExisting(t *testing.T) {
	// OVNEnsureACLs loads each of these statuses before the planner, and applies
	// each non-nil aclInfo afterward. The last two do not require reapplyRules.
	for _, branch := range []string{"reapply", "no-rules", "added-network"} {
		t.Run(branch, func(t *testing.T) {
			status := ovnACLStatus{ovnACLReference: ovnACLReference{name: "existing", id: 1}, aclInfo: referenceRules("ingress-ref", "egress-ref")}
			if branch == "added-network" {
				status.addACLNets = map[string]NetworkACLUsage{"new-network": {}}
			}

			existing := []ovnACLStatus{status, {ovnACLReference: ovnACLReference{name: "unchanged", id: 2}}}
			got, err := ovnPlanReferencedACLs(nil, existing, map[string]int64{"ingress-ref": 91, "egress-ref": 87})
			if err != nil {
				t.Fatal(err)
			}

			want := []ovnACLReference{{name: "egress-ref", id: 87}, {name: "ingress-ref", id: 91}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s loaded rules references = %v; want %v", branch, got, want)
			}
		})
	}
}

func TestOVNReferencedACLPlannerCreateAndExisting(t *testing.T) {
	create := []ovnACLStatus{
		{ovnACLReference: ovnACLReference{name: "first", id: 11}, aclInfo: referenceRules("first, second, shared, office, $office", "other, shared")},
		{ovnACLReference: ovnACLReference{name: "second", id: 12}, aclInfo: referenceRules("first, shared", "second")},
	}

	existing := []ovnACLStatus{
		{ovnACLReference: ovnACLReference{name: "third", id: 13}, aclInfo: referenceRules("shared, first", "loaded-only"), addACLNets: map[string]NetworkACLUsage{"net": {}}},
		{ovnACLReference: ovnACLReference{name: "unchanged", id: 14}},
	}

	catalog := map[string]int64{"first": 11, "second": 12, "shared": 101, "office": 102, "other": 103, "loaded-only": 104}
	before, err := json.Marshal([]any{create[0].aclInfo, create[1].aclInfo, existing[0].aclInfo, existing[0].addACLNets, catalog})
	if err != nil {
		t.Fatal(err)
	}

	got, err := ovnPlanReferencedACLs(create, existing, catalog)
	if err != nil {
		t.Fatal(err)
	}

	want := []ovnACLReference{{name: "loaded-only", id: 104}, {name: "office", id: 102}, {name: "other", id: 103}, {name: "shared", id: 101}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v; want %v", got, want)
	}

	after, err := json.Marshal([]any{create[0].aclInfo, create[1].aclInfo, existing[0].aclInfo, existing[0].addACLNets, catalog})
	if err != nil {
		t.Fatal(err)
	}

	if string(before) != string(after) || create[0].id != 11 || create[1].name != "second" || existing[1].aclInfo != nil {
		t.Fatal("planner mutated its inputs")
	}

	// Results carry checked IDs independently of any later changes to the catalog.
	catalog["shared"] = 999
	if !reflect.DeepEqual(got, want) {
		t.Fatal("plan did not retain checked IDs")
	}
}

func TestOVNResolveACLReferences(t *testing.T) {
	for _, tc := range []struct {
		name    string
		catalog map[string]int64
		wantErr string
	}{
		{name: "missing", catalog: map[string]int64{"good": 31}, wantErr: "Cannot find"},
		{name: "zero", catalog: map[string]int64{"good": 31, "bad": 0}, wantErr: "Invalid"},
		{name: "negative", catalog: map[string]int64{"good": 31, "bad": -7}, wantErr: "Invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ovnResolveACLReferences([]string{"good", "bad"}, tc.catalog)
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), `"bad"`) {
				t.Fatalf("invalid direct identity: plan=%v err=%v", got, err)
			}
		})
	}

	got, err := ovnResolveACLReferences([]string{"second", "first", "second"}, map[string]int64{"first": 17, "second": 29})
	want := []ovnACLReference{{name: "second", id: 29}, {name: "first", id: 17}, {name: "second", id: 29}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("valid direct identities: plan=%v err=%v; want %v", got, err, want)
	}
}

func TestOVNReferencedACLPlannerInvalidReferences(t *testing.T) {
	for _, tc := range []struct {
		name    string
		catalog map[string]int64
	}{
		{name: "missing", catalog: map[string]int64{"a-valid": 23}},
		{name: "zero", catalog: map[string]int64{"a-valid": 23, "z-invalid": 0}},
		{name: "negative", catalog: map[string]int64{"a-valid": 23, "z-invalid": -1}},
	} {
		for _, branch := range []string{"create", "existing"} {
			t.Run(tc.name+"/"+branch, func(t *testing.T) {
				status := []ovnACLStatus{{ovnACLReference: ovnACLReference{name: "requested", id: 1}, aclInfo: referenceRules("a-valid, z-invalid", "")}}
				var create, existing []ovnACLStatus
				if branch == "create" {
					create = status
				} else {
					existing = status
				}

				got, err := ovnPlanReferencedACLs(create, existing, tc.catalog)
				if got != nil || err == nil || !strings.Contains(err.Error(), `"z-invalid"`) {
					t.Fatalf("invalid reference must discard whole plan: plan=%v err=%v", got, err)
				}
			})
		}
	}
}

func TestOVNReferencedACLPlannerInvalidCreationID(t *testing.T) {
	for _, id := range []int64{0, -1} {
		create := []ovnACLStatus{{ovnACLReference: ovnACLReference{name: "self", id: id}, aclInfo: referenceRules("self", "")}}
		got, err := ovnPlanReferencedACLs(create, nil, map[string]int64{"self": id})
		if got != nil || err == nil || !strings.Contains(err.Error(), `"self"`) {
			t.Fatalf("invalid creation ID must not be excluded: plan=%v err=%v", got, err)
		}
	}
}

func TestOVNReferencedACLPlannerEmpty(t *testing.T) {
	got, err := ovnResolveACLReferences(nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty direct plan=%v err=%v", got, err)
	}

	got, err = ovnPlanReferencedACLs(nil, []ovnACLStatus{{ovnACLReference: ovnACLReference{name: "unchanged", id: 4}}}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty reference plan=%v err=%v", got, err)
	}

	create := []ovnACLStatus{{ovnACLReference: ovnACLReference{name: "self", id: 3}, aclInfo: referenceRules("self", "")}}
	got, err = ovnPlanReferencedACLs(create, nil, map[string]int64{"self": 3})
	if err != nil || len(got) != 0 {
		t.Fatalf("self-only reference plan=%v err=%v", got, err)
	}
}
