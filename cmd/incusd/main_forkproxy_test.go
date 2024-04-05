package main

import (
	"log"
	"testing"

	"github.com/stretchr/testify/require"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/network"
)

func TestParseAddr(t *testing.T) {
	tests := []struct {
		name       string
		address    string
		expected   *deviceConfig.ProxyAddress
		shouldFail bool
	}{
		// Port testing
		{
			"Single port",
			"tcp:127.0.0.1:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Multiple ports",
			"tcp:127.0.0.1:2000,2002",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports: []uint64{
					2000,
					2002,
				},
				Abstract: false,
			},
			false,
		},
		{
			"Port range",
			"tcp:127.0.0.1:2000-2002",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports: []uint64{
					2000,
					2001,
					2002,
				},
				Abstract: false,
			},
			false,
		},
		{
			"Mixed ports and port ranges",
			"tcp:127.0.0.1:2000,2002,3000-3003,4000-4003",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports: []uint64{
					2000,
					2002,
					3000,
					3001,
					3002,
					3003,
					4000,
					4001,
					4002,
					4003,
				},
				Abstract: false,
			},
			false,
		},
		// connType testing
		{
			"UDP",
			"udp:127.0.0.1:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "udp",
				Address:  "127.0.0.1",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Unix socket",
			"unix:/foobar",
			&deviceConfig.ProxyAddress{
				ConnType: "unix",
				Address:  "/foobar",
				Abstract: false,
			},
			false,
		},
		{
			"Abstract unix socket",
			"unix:@/foobar",
			&deviceConfig.ProxyAddress{
				ConnType: "unix",
				Address:  "@/foobar",
				Abstract: true,
			},
			false,
		},
		{
			"Unknown connection type",
			"bla:blub",
			nil,
			true,
		},
		// Address testing
		{
			"Valid IPv6 address (1)",
			"tcp:[fd39:2561:7238:91b5:0:0:0:0]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "fd39:2561:7238:91b5:0:0:0:0",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Valid IPv6 address (2)",
			"tcp:[fd39:2561:7238:91b5::0]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "fd39:2561:7238:91b5::0",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Valid IPv6 address (3)",
			"tcp:[::1]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "::1",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Valid IPv6 address (4)",
			"tcp:[::]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "::",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Invalid IPv6 address (1)",
			"tcp:fd39:2561:7238:91b5:0:0:0:0:2000",
			nil,
			true,
		},
		{
			"Invalid IPv6 address (2)",
			"tcp:fd39:2561:7238:91b5::0:2000",
			nil,
			true,
		},
	}

	for i, tt := range tests {
		log.Printf("Running test #%d: %s", i, tt.name)
		addr, err := network.ProxyParseAddr(tt.address)
		if tt.shouldFail {
			require.Error(t, err)
			require.Nil(t, addr)
			continue
		}

		require.NoError(t, err)
		require.Equal(t, tt.expected, addr)
	}
}
