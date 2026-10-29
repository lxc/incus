package device

import (
	"errors"
	"fmt"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// networkBridgeVLANList behaves like netlink’s BridgeVlanList, except it uses
// `RTEXT_FILTER_BRVLAN_COMPRESSED` to get a compact view of VLAN ranges.
func networkBridgeVLANList() (map[int32][]*nl.BridgeVlanInfo, error) {
	req := nl.NewNetlinkRequest(unix.RTM_GETLINK, unix.NLM_F_DUMP)
	req.AddData(nl.NewIfInfomsg(unix.AF_BRIDGE))
	req.AddData(nl.NewRtAttr(unix.IFLA_EXT_MASK, nl.Uint32Attr(uint32(nl.RTEXT_FILTER_BRVLAN_COMPRESSED))))

	msgs, executeErr := req.Execute(unix.NETLINK_ROUTE, unix.RTM_NEWLINK)
	if executeErr != nil && !errors.Is(executeErr, netlink.ErrDumpInterrupted) {
		return nil, executeErr
	}

	ret := make(map[int32][]*nl.BridgeVlanInfo)
	for _, m := range msgs {
		msg := nl.DeserializeIfInfomsg(m)

		attrs, err := nl.ParseRouteAttr(m[msg.Len():])
		if err != nil {
			return nil, err
		}

		for _, attr := range attrs {
			if attr.Attr.Type != unix.IFLA_AF_SPEC {
				continue
			}

			nestAttrs, err := nl.ParseRouteAttr(attr.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to parse nested attr: %w", err)
			}

			for _, nestAttr := range nestAttrs {
				if nestAttr.Attr.Type != nl.IFLA_BRIDGE_VLAN_INFO {
					continue
				}

				vlanInfo := nl.DeserializeBridgeVlanInfo(nestAttr.Value)
				ret[msg.Index] = append(ret[msg.Index], vlanInfo)
			}
		}
	}

	return ret, executeErr
}
