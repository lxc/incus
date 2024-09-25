package drivers

import (
	"text/template"
)

var nftablesCommonTable = template.Must(template.New("nftablesCommonTable").Parse(`
table {{.family}} {{.namespace}} {
	{{- template "nftablesContent" . -}}
}
`))

var nftablesNetForwardingPolicy = template.Must(template.New("nftablesNetForwardingPolicy").Parse(`
chain fwd{{.chainSeparator}}{{.networkName}} {
	type filter hook forward priority 0; policy accept;

	{{if .ip4Action -}}
	ip version 4 oifname "{{.networkName}}" {{.ip4Action}}
	ip version 4 iifname "{{.networkName}}" {{.ip4Action}}
	{{- end}}

	{{if .ip6Action -}}
	ip6 version 6 oifname "{{.networkName}}" {{.ip6Action}}
	ip6 version 6 iifname "{{.networkName}}" {{.ip6Action}}
	{{- end}}
}
`))

var nftablesNetOutboundNAT = template.Must(template.New("nftablesNetOutboundNAT").Parse(`
chain pstrt{{.chainSeparator}}{{.networkName}} {
	type nat hook postrouting priority 100; policy accept;

	{{- range $ipFamily, $config := .rules}}
	{{if $config.SNATAddress -}}
	{{$ipFamily}} saddr {{$config.Subnet}} {{$ipFamily}} daddr != {{$config.Subnet}} snat {{$config.SNATAddress}}
	{{else -}}
	{{$ipFamily}} saddr {{$config.Subnet}} {{$ipFamily}} daddr != {{$config.Subnet}} masquerade
	{{- end}}
	{{- end}}
}
`))

var nftablesNetICMPDHCPDNS = template.Must(template.New("nftablesNetDHCPDNS").Parse(`
chain in{{.chainSeparator}}{{.networkName}} {
	type filter hook input priority 0; policy accept;

	iifname "{{.networkName}}" tcp dport 53 accept
	iifname "{{.networkName}}" udp dport 53 accept

	{{- range .ipFamilies}}
	{{if eq . "ip" -}}
	iifname "{{$.networkName}}" icmp type {3, 11, 12} accept
	iifname "{{$.networkName}}" udp dport 67 accept
	{{else -}}
	iifname "{{$.networkName}}" icmpv6 type {1, 2, 3, 4, 133, 135, 136, 143} accept
	iifname "{{$.networkName}}" udp dport 547 accept
	{{- end}}
	{{- end}}
}

chain out{{.chainSeparator}}{{.networkName}} {
	type filter hook output priority 0; policy accept;

	oifname "{{.networkName}}" tcp sport 53 accept
	oifname "{{.networkName}}" udp sport 53 accept

	{{- range .ipFamilies}}
	{{if eq . "ip" -}}
	oifname "{{$.networkName}}" icmp type {3, 11, 12} accept
	oifname "{{$.networkName}}" udp sport 67 accept
	{{else -}}
	oifname "{{$.networkName}}" icmpv6 type {1, 2, 3, 4, 128, 134, 135, 136, 143}  accept
	oifname "{{$.networkName}}" udp sport 547 accept
	{{- end}}
	{{- end}}
}
`))

var nftablesNetProxyNAT = template.Must(template.New("nftablesNetProxyNAT").Parse(`
add table {{.family}} {{.namespace}}
add chain {{.family}} {{.namespace}} {{.chainPrefix}}prert{{.chainSeparator}}{{.label}} {type nat hook prerouting priority -100; policy accept;}
add chain {{.family}} {{.namespace}} {{.chainPrefix}}out{{.chainSeparator}}{{.label}} {type nat hook output priority -100; policy accept;}
add chain {{.family}} {{.namespace}} {{.chainPrefix}}pstrt{{.chainSeparator}}{{.label}} {type nat hook postrouting priority 100; policy accept;}
flush chain {{.family}} {{.namespace}} {{.chainPrefix}}prert{{.chainSeparator}}{{.label}}
flush chain {{.family}} {{.namespace}} {{.chainPrefix}}out{{.chainSeparator}}{{.label}}
flush chain {{.family}} {{.namespace}} {{.chainPrefix}}pstrt{{.chainSeparator}}{{.label}}

table {{.family}} {{.namespace}} {
	chain {{.chainPrefix}}prert{{.chainSeparator}}{{.label}} {
		type nat hook prerouting priority -100; policy accept;
		{{- range .dnatRules}}
		{{.ipFamily}} daddr {{.listenAddress}} {{if .protocol}}{{.protocol}} dport {{.listenPorts}}{{end}} dnat to {{.targetDest}}
		{{- end}}
	}

	chain {{.chainPrefix}}out{{.chainSeparator}}{{.label}} {
		type nat hook output priority -100; policy accept;
		{{- range .dnatRules}}
		{{.ipFamily}} daddr {{.listenAddress}} {{if .protocol}}{{.protocol}} dport {{.listenPorts}}{{end}} dnat to {{.targetDest}}
		{{- end}}
	}

	chain {{.chainPrefix}}pstrt{{.chainSeparator}}{{.label}} {
		type nat hook postrouting priority 100; policy accept;
		{{- range .snatRules}}
		{{.ipFamily}} saddr {{.targetHost}} {{.ipFamily}} daddr {{.targetHost}} {{if .protocol}}{{.protocol}} dport {{.targetPorts}}{{end}} masquerade
		{{- end}}
	}
}
`))

var nftablesNetACLSetup = template.Must(template.New("nftablesNetACLSetup").Parse(`
add table {{.family}} {{.namespace}}
add chain {{.family}} {{.namespace}} acl{{.chainSeparator}}{{.networkName}}
add chain {{.family}} {{.namespace}} aclin{{.chainSeparator}}{{.networkName}} {type filter hook input priority filter; policy accept;}
add chain {{.family}} {{.namespace}} aclout{{.chainSeparator}}{{.networkName}} {type filter hook output priority filter; policy accept;}
add chain {{.family}} {{.namespace}} aclfwd{{.chainSeparator}}{{.networkName}} {type filter hook forward priority filter; policy accept;}
flush chain {{.family}} {{.namespace}} acl{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} aclin{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} aclout{{.chainSeparator}}{{.networkName}}
flush chain {{.family}} {{.namespace}} aclfwd{{.chainSeparator}}{{.networkName}}

table {{.family}} {{.namespace}} {
	chain aclin{{.chainSeparator}}{{.networkName}} {
		# Allow DNS to Incus host.
		iifname "{{.networkName}}" tcp dport 53 accept
		iifname "{{.networkName}}" udp dport 53 accept

		# Allow DHCPv6 to Incus host.
		iifname "{{$.networkName}}" udp dport 67 accept
		iifname "{{$.networkName}}" udp dport 547 accept

		# Allow core ICMPv4 to Incus host.
		iifname "{{$.networkName}}" icmp type {3, 11, 12} accept

		# Allow core ICMPv6 to Incus host.
		iifname "{{$.networkName}}" icmpv6 type {1, 2, 3, 4, 133, 135, 136, 143} accept

		iifname "{{.networkName}}" jump acl{{.chainSeparator}}{{.networkName}}
	}

	chain aclout{{.chainSeparator}}{{.networkName}} {
		# Allow DHCPv6 from Incus host.
		oifname "{{$.networkName}}" udp sport 67 accept
		oifname "{{$.networkName}}" udp sport 547 accept

		# Allow core ICMPv4 from Incus host.
		oifname "{{$.networkName}}" icmp type {3, 11, 12} accept

		# Allow ICMPv6 ping from host into network as dnsmasq uses this to probe IP allocations.
		oifname "{{$.networkName}}" icmpv6 type {1, 2, 3, 4, 128, 134, 135, 136, 143}  accept

		oifname "{{.networkName}}" jump acl{{.chainSeparator}}{{.networkName}}
	}

	chain aclfwd{{.chainSeparator}}{{.networkName}} {
		iifname "{{.networkName}}" jump acl{{.chainSeparator}}{{.networkName}}
		oifname "{{.networkName}}" jump acl{{.chainSeparator}}{{.networkName}}
	}
}
`))

var nftablesNetACLRules = template.Must(template.New("nftablesNetACLRules").Parse(`
flush chain {{.family}} {{.namespace}} acl{{.chainSeparator}}{{.networkName}}

table {{.family}} {{.namespace}} {
	chain acl{{.chainSeparator}}{{.networkName}} {
                ct state established,related accept

		{{- range .rules}}
		{{.}}
		{{- end}}
	}
}
`))

// nftablesInstanceBridgeFilter defines the rules needed for MAC, IPv4 and IPv6 bridge security filtering.
// To prevent instances from using IPs that are different from their assigned IPs we use ARP and NDP filtering
// to prevent neighbour advertisements that are not allowed. However in order for DHCPv4 & DHCPv6 to work back to
// the Incus host we need to allow DHCPv4 inbound and for IPv6 we need to allow IPv6 Router Solicitation and DHPCv6.
// Nftables doesn't support the equivalent of "arp saddr" and "arp saddr ether" at this time so in order to filter
// NDP advertisements that come from the genuine Ethernet MAC address but have a spoofed NDP source MAC/IP address
// we need to use manual header offset extraction. This also drops IPv6 router advertisements from instance.
// If IP filtering is enabled, this also drops unwanted ethernet frames.
var nftablesInstanceBridgeFilter = template.Must(template.New("nftablesInstanceBridgeFilter").Parse(`
chain in{{.chainSeparator}}{{.deviceLabel}} {
	type filter hook input priority -200; policy accept;
	iifname "{{.hostName}}" ether saddr != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type arp arp saddr ether != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 136 @nh,528,48 != {{.hwAddrHex}} drop
	{{if .ipv4Nets -}}
	iifname "{{.hostName}}" ether type ip ip saddr 0.0.0.0 ip daddr 255.255.255.255 udp dport 67 accept
	{{range .ipv4Nets -}}
	iifname "{{$.hostName}}" ether type arp arp saddr ip {{.}} accept
	iifname "{{$.hostName}}" ether type ip ip saddr {{.}} accept
	{{end}}
	iifname "{{.hostName}}" ether type arp drop
	iifname "{{.hostName}}" ether type ip drop
	{{- end}}
	{{if .ipv4FilterAll -}}
	iifname "{{.hostName}}" ether type arp drop
	iifname "{{.hostName}}" ether type ip drop
	{{- end}}
	{{if .ipv6Nets -}}
	iifname "{{.hostName}}" ether type ip6 ip6 saddr fe80::/10 ip6 daddr ff02::1:2 udp dport 547 accept
	iifname "{{.hostName}}" ether type ip6 ip6 saddr fe80::/10 ip6 daddr ff02::2 icmpv6 type 133 accept
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 134 drop
	{{ range .ipv6Nets -}}
	iifname "{{$.hostName}}" ether type ip6 icmpv6 type 136 @nh,384,{{.nBits}} {{.hexPrefix}} accept
	iifname "{{$.hostName}}" ether type ip6 ip6 saddr {{.net}} accept
	{{end}}
	iifname "{{.hostName}}" ether type ip6 drop
	{{- end}}
	{{if .ipv6FilterAll -}}
	iifname "{{.hostName}}" ether type ip6 drop
	{{- end}}
	{{if .filterUnwantedFrames -}}
	iifname "{{.hostName}}" ether type != {arp, ip, ip6} drop
	{{- end}}
}

chain fwd{{.chainSeparator}}{{.deviceLabel}} {
	type filter hook forward priority -200; policy accept;
	iifname "{{.hostName}}" ether saddr != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type arp arp saddr ether != {{.hwAddr}} drop
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 136 @nh,528,48 != {{.hwAddrHex}} drop
	{{if .ipv4Nets -}}
	{{range .ipv4Nets -}}
	iifname "{{$.hostName}}" ether type arp arp saddr ip {{.}} accept
	iifname "{{$.hostName}}" ether type ip ip saddr {{.}} accept
	{{end}}
	iifname "{{.hostName}}" ether type arp drop
	iifname "{{.hostName}}" ether type ip drop
	{{end}}
	{{if .ipv4FilterAll -}}
	iifname "{{.hostName}}" ether type arp drop
	iifname "{{.hostName}}" ether type ip drop
	{{- end}}
	{{if .ipv6Nets -}}
	iifname "{{.hostName}}" ether type ip6 icmpv6 type 134 drop
	{{range .ipv6Nets}}
	iifname "{{$.hostName}}" ether type ip6 ip6 saddr {{.net}} accept
	iifname "{{$.hostName}}" ether type ip6 icmpv6 type 136 @nh,384,{{.nBits}} {{.hexPrefix}} accept
	{{end}}
	iifname "{{.hostName}}" ether type ip6 drop
	{{- end}}
	{{if .ipv6FilterAll -}}
	iifname "{{.hostName}}" ether type ip6 drop
	{{- end}}
	{{if .filterUnwantedFrames -}}
	iifname "{{.hostName}}" ether type != {arp, ip, ip6} drop
	{{- end}}
}
`))

// nftablesInstanceRPFilter defines the rules to perform reverse path filtering.
var nftablesInstanceRPFilter = template.Must(template.New("nftablesInstanceRPFilter").Parse(`
chain prert{{.chainSeparator}}{{.deviceLabel}} {
	type filter hook prerouting priority -300; policy accept;
	iif "{{.hostName}}" fib saddr . iif oif missing drop
}
`))

// nftablesInstanceNetPrio defines the rules to perform setting of skb->priority.
var nftablesInstanceNetPrio = template.Must(template.New("nftablesInstanceNetPrio").Parse(`
chain egress{{.chainSeparator}}netprio{{.chainSeparator}}{{.deviceLabel}} {
	type filter hook egress device "{{.deviceName}}" priority 0 ;
	meta priority set "{{.netPrio}}"
}
`))
