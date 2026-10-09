package acl

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
	"github.com/lxc/incus/v7/shared/validate"
)

// ovnACLReference captures the catalog identity checked before port-group creation.
type ovnACLReference struct {
	name string
	id   int64
}

// ovnACLStatus holds the rules and per-network groups needed by OVNEnsureACLs.
// Its embedded identity comes from ovnResolveACLReferences before any existence read.
type ovnACLStatus struct {
	ovnACLReference
	aclInfo    *api.NetworkACL
	addACLNets map[string]NetworkACLUsage
}

// ovnResolveACLReferences validates the whole supplied catalog selection without side effects.
func ovnResolveACLReferences(names []string, aclNameIDs map[string]int64) ([]ovnACLReference, error) {
	references := make([]ovnACLReference, 0, len(names))
	for _, name := range names {
		id, found := aclNameIDs[name]
		if !found {
			return nil, fmt.Errorf("Cannot find security ACL ID for %q", name)
		}

		if id <= 0 {
			return nil, fmt.Errorf("Invalid security ACL ID %d for %q", id, name)
		}

		references = append(references, ovnACLReference{name: name, id: id})
	}

	return references, nil
}

// ovnPlanReferencedACLs selects direct rule dependencies for every ruleset to be applied.
// It does not load referenced rules or mutate the supplied statuses or catalog.
func ovnPlanReferencedACLs(create []ovnACLStatus, existing []ovnACLStatus, aclNameIDs map[string]int64) ([]ovnACLReference, error) {
	referencedACLs := make(map[string]struct{})
	for _, status := range create {
		ovnAddReferencedACLs(status.aclInfo, referencedACLs)
	}

	for _, status := range existing {
		if status.aclInfo != nil {
			ovnAddReferencedACLs(status.aclInfo, referencedACLs)
		}
	}

	// Creation groups are already planned, but only omit a positively checked identity.
	for _, status := range create {
		if status.id <= 0 {
			return nil, fmt.Errorf("Invalid security ACL ID %d for %q", status.id, status.name)
		}

		delete(referencedACLs, status.name)
	}

	names := make([]string, 0, len(referencedACLs))
	for name := range referencedACLs {
		names = append(names, name)
	}

	slices.Sort(names)
	return ovnResolveACLReferences(names, aclNameIDs)
}

// ovnAddReferencedACLs adds to the referencedACLNames any ACLs referenced by the rules in the supplied ACL.
func ovnAddReferencedACLs(info *api.NetworkACL, referencedACLNames map[string]struct{}) {
	addACLNamesFrom := func(ruleSubjects []string) {
		for _, subject := range ruleSubjects {
			if subject == "" {
				continue // Whitespace-only comma segments are not ACL names.
			}

			_, found := referencedACLNames[subject]
			if found {
				continue // Skip subjects already seen.
			}

			if slices.Contains(ruleSubjectInternalAliases, subject) || slices.Contains(ruleSubjectExternalAliases, subject) ||
				strings.HasPrefix(subject, "@") || strings.HasPrefix(subject, "$") {
				continue // Reserved, peer and address-set subjects are not ACL names.
			}

			if validate.IsNetworkAddress(subject) == nil || validate.IsNetworkAddressCIDR(subject) == nil || validate.IsNetworkRange(subject) == nil {
				continue // Skip IP addresses, CIDRs and ranges.
			}

			// Anything else must be a referenced ACL name.
			referencedACLNames[subject] = struct{}{}
		}
	}

	for _, rule := range info.Ingress {
		addACLNamesFrom(util.SplitNTrimSpace(rule.Source, ",", -1, true))
	}

	for _, rule := range info.Egress {
		addACLNamesFrom(util.SplitNTrimSpace(rule.Destination, ",", -1, true))
	}
}
