package ovn

// OVS TCP Flags from OVS lib/packets.h.
const (
	TCPFIN = 0x001
	TCPSYN = 0x002
	TCPRST = 0x004
	TCPPSH = 0x008
	TCPACK = 0x010
	TCPURG = 0x020
	TCPECE = 0x040
	TCPCWR = 0x080
	TCPNS  = 0x100
)
