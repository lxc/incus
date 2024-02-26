package device

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/network/acl"
	"github.com/lxc/incus/shared/validate"
)

// nicValidationRules returns config validation rules for nic devices.
func nicValidationRules(requiredFields []string, optionalFields []string, instConf instance.ConfigReader) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		"acceleration":                         validate.Optional(validate.IsOneOf("none", "sriov", "vdpa")),
		"name":                                 validate.Optional(validate.IsInterfaceName, func(_ string) error { return nicCheckNamesUnique(instConf) }),
		"parent":                               validate.IsAny,
		"network":                              validate.IsAny,
		"mtu":                                  validate.Optional(validate.IsNetworkMTU),
		"vlan":                                 validate.IsNetworkVLAN,
		"gvrp":                                 validate.Optional(validate.IsBool),
		"hwaddr":                               validate.IsNetworkMAC,
		"host_name":                            validate.IsAny,
		"limits.ingress":                       validate.IsAny,
		"limits.egress":                        validate.IsAny,
		"limits.max":                           validate.IsAny,
		"limits.priority":                      validate.Optional(validate.IsUint32),
		"security.mac_filtering":               validate.IsAny,
		"security.ipv4_filtering":              validate.IsAny,
		"security.ipv6_filtering":              validate.IsAny,
		"security.port_isolation":              validate.Optional(validate.IsBool),
		"ipv4.address":                         validate.Optional(validate.IsNetworkAddressV4),
		"ipv6.address":                         validate.Optional(validate.IsNetworkAddressV6),
		"ipv4.routes":                          validate.Optional(validate.IsListOf(validate.IsNetworkV4)),
		"ipv6.routes":                          validate.Optional(validate.IsListOf(validate.IsNetworkV6)),
		"boot.priority":                        validate.Optional(validate.IsUint32),
		"ipv4.gateway":                         networkValidGateway,
		"ipv6.gateway":                         networkValidGateway,
		"ipv4.host_address":                    validate.Optional(validate.IsNetworkAddressV4),
		"ipv6.host_address":                    validate.Optional(validate.IsNetworkAddressV6),
		"ipv4.host_table":                      validate.Optional(validate.IsUint32),
		"ipv6.host_table":                      validate.Optional(validate.IsUint32),
		"queue.tx.length":                      validate.Optional(validate.IsUint32),
		"ipv4.routes.external":                 validate.Optional(validate.IsListOf(validate.IsNetworkV4)),
		"ipv6.routes.external":                 validate.Optional(validate.IsListOf(validate.IsNetworkV6)),
		"nested":                               validate.IsAny,
		"security.acls":                        validate.IsAny,
		"security.acls.default.ingress.action": validate.Optional(validate.IsOneOf(acl.ValidActions...)),
		"security.acls.default.egress.action":  validate.Optional(validate.IsOneOf(acl.ValidActions...)),
		"security.acls.default.ingress.logged": validate.Optional(validate.IsBool),
		"security.acls.default.egress.logged":  validate.Optional(validate.IsBool),
	}

	validators := map[string]func(value string) error{}

	for _, k := range optionalFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in an empty check as field is optional.
		validators[k] = func(value string) error {
			if value == "" {
				return nil
			}

			return defaultValidator(value)
		}
	}

	// Add required fields last, that way if they are specified in both required and optional
	// field sets, the required one will overwrite the optional validators.
	for _, k := range requiredFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in a not empty check as field is required.
		validators[k] = func(value string) error {
			err := validate.IsNotEmpty(value)
			if err != nil {
				return err
			}

			return defaultValidator(value)
		}
	}

	return validators
}

// nicHasAutoGateway takes the value of the "ipv4.gateway" or "ipv6.gateway" config keys and returns whether they
// specify whether the gateway mode is automatic or not.
func nicHasAutoGateway(value string) bool {
	if value == "" || value == "auto" {
		return true
	}

	return false
}

// nicCheckNamesUnique checks that all the NICs in the instConf's expanded devices have a unique (or unset) name.
func nicCheckNamesUnique(instConf instance.ConfigReader) error {
	seenNICNames := []string{}

	for _, devConfig := range instConf.ExpandedDevices() {
		if devConfig["type"] != "nic" || devConfig["name"] == "" {
			continue
		}

		if slices.Contains(seenNICNames, devConfig["name"]) {
			return fmt.Errorf("Duplicate NIC name detected %q", devConfig["name"])
		}

		seenNICNames = append(seenNICNames, devConfig["name"])
	}

	return nil
}

// nicCheckDNSNameConflict returns if instNameA matches instNameB (case insensitive).
func nicCheckDNSNameConflict(instNameA string, instNameB string) bool {
	return strings.EqualFold(instNameA, instNameB)
}
