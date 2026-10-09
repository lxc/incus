package ovn

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// NewNICStartPrefixOwner captures a normal Start's producer snapshot. A newly enabled port can
// acquire a northd up update before publication, so guard its content as normal Add does.
// Cleanup and migration capture continue to use NewNICPrefixOwner and its strict version proof.
func (o *NB) NewNICStartPrefixOwner(ctx context.Context, sw OVNSwitch, port OVNSwitchPort, source string) (NICPrefixOwner, error) {
	plan, row, err := o.publicationPort(ctx, sw, port)
	if err != nil || row == nil {
		return NICPrefixOwner{}, errors.Join(err, errors.New("NIC Start allocation port is absent or ambiguous"))
	}

	rows, err := o.nicPrefixRead(ctx, "Logical_Switch_Port", "_uuid", ovsdb.UUID{GoUUID: plan.PortUUID})
	if err != nil || len(rows) != 1 {
		return NICPrefixOwner{}, errors.Join(err, errors.New("NIC Start allocation port is absent or ambiguous"))
	}

	row = rows[0]
	external, err := nicCleanupStringMap(row["external_ids"])
	if err != nil || row["name"] != string(port) || external[ovnExtIDIncusLocation] != source {
		return NICPrefixOwner{}, errors.New("NIC Start allocation port source changed")
	}

	version, err := nicCleanupRowUUID(row, "_version")
	if err != nil {
		return NICPrefixOwner{}, err
	}

	content, err := nicStartPortDigest(row)
	if err != nil {
		return NICPrefixOwner{}, err
	}

	plan.Source = source
	owner := NICPrefixOwner{RootUUID: plan.RootUUID, SwitchUUID: plan.SwitchUUID, SwitchName: sw, PortUUID: plan.PortUUID, PortName: port, Generation: uuid.NewString(), PortVersion: version, StartContent: content, Source: source, Previous: nicPrefixDigest(external[nicPrefixGeneration])}
	err = owner.Validate(o.backendID, plan)
	if err != nil {
		return NICPrefixOwner{}, err
	}

	_, err = o.nicCleanupTransact(ctx, nicCleanupRootWait(o.backendID), nicCleanupPortParentWait(plan), nicCleanupPortOwnerWait(plan), nicPublicationRowWait(row))
	return owner, err
}

// nicStartPortDigest canonicalizes the complete producer row. OVSDB sets and maps have no ordering;
// up belongs to northd, and _version changes with that derived update. All other columns, including
// the actual row UUID, source, enabled state, addresses, DHCP references and generation, are pinned.
func nicStartPortDigest(row ovsdb.Row) (string, error) {
	content := map[string]json.RawMessage{}
	for column, value := range row {
		if column == "up" || column == "_version" {
			continue
		}

		entries := []string{}
		switch value := value.(type) {
		case ovsdb.OvsSet:
			for _, entry := range value.GoSet {
				encoded, err := json.Marshal(entry)
				if err != nil {
					return "", err
				}

				entries = append(entries, string(encoded))
			}

			slices.Sort(entries)
		case ovsdb.OvsMap:
			for key, entry := range value.GoMap {
				encoded, err := json.Marshal([]any{key, entry})
				if err != nil {
					return "", err
				}

				entries = append(entries, string(encoded))
			}

			slices.Sort(entries)
		}

		var encoded []byte
		var err error
		switch value.(type) {
		case ovsdb.OvsSet:
			encoded, err = json.Marshal([]any{"set", entries})
		case ovsdb.OvsMap:
			encoded, err = json.Marshal([]any{"map", entries})
		default:
			encoded, err = json.Marshal(value)
		}

		if err != nil {
			return "", err
		}

		content[column] = encoded
	}

	encoded, err := json.Marshal(content)
	if err != nil {
		return "", err
	}

	return nicPrefixDigest(string(encoded)), nil
}
