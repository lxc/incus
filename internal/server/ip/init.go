package ip

import (
	"github.com/vishvananda/netlink/nl"
)

func init() {
	nl.EnableErrorMessageReporting = true
}
