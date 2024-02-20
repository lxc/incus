package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/shared/subprocess"
)

func ovsConvert() ([][]string, error) {
	commands := [][]string{}

	output, err := subprocess.RunCommand("ovs-vsctl", "get", "open_vswitch", ".", "external-ids:ovn-bridge-mappings")
	if err != nil {
		// Assume that being unable to read the key means it's not set.
		return nil, nil
	}

	oldValue := strings.TrimSpace(strings.Replace(output, "\"", "", -1))
	oldBridges := []string{}

	values := strings.Split(oldValue, ",")
	for i, value := range values {
		fields := strings.Split(value, ":")
		oldBridges = append(oldBridges, fields[1])
		fields[1] = strings.Replace(fields[1], "lxdovn", "incusovn", -1)
		values[i] = strings.Join(fields, ":")
	}

	newValue := strings.Join(values, ",")

	if oldValue != newValue {
		commands = append(commands, []string{"ovs-vsctl", "set", "open_vswitch", ".", fmt.Sprintf("external-ids:ovn-bridge-mappings=%s", newValue)})
	}

	for _, bridge := range oldBridges {
		commands = append(commands, []string{"ovs-vsctl", "del-br", bridge})
	}

	return commands, nil
}

func ovnBackup(nbDB string, sbDB string, target string) error {
	// Backup the Northbound database.
	nbStdout, err := os.Create(filepath.Join(target, fmt.Sprintf("lxd-to-incus.ovn-nb.%d.backup", os.Getpid())))
	if err != nil {
		return err
	}

	defer nbStdout.Close()

	err = nbStdout.Chmod(0600)
	if err != nil {
		return err
	}

	args := []string{"dump", "-f", "csv", nbDB, "OVN_Northbound"}
	if strings.Contains(nbDB, "ssl:") {
		args = append(args, "-c", "/etc/ovn/cert_host")
		args = append(args, "-p", "/etc/ovn/key_host")
		args = append(args, "-C", "/etc/ovn/ovn-central.crt")
	}

	err = subprocess.RunCommandWithFds(context.Background(), nil, nbStdout, "ovsdb-client", args...)
	if err != nil {
		return err
	}

	// Backup the Southbound database.
	sbStdout, err := os.Create(filepath.Join(target, fmt.Sprintf("lxd-to-incus.ovn-sb.%d.backup", os.Getpid())))
	if err != nil {
		return err
	}

	defer sbStdout.Close()

	err = sbStdout.Chmod(0600)
	if err != nil {
		return err
	}

	args = []string{"dump", "-f", "csv", sbDB, "OVN_Southbound"}
	if strings.Contains(sbDB, "ssl:") {
		args = append(args, "-c", "/etc/ovn/cert_host")
		args = append(args, "-p", "/etc/ovn/key_host")
		args = append(args, "-C", "/etc/ovn/ovn-central.crt")
	}

	err = subprocess.RunCommandWithFds(context.Background(), nil, sbStdout, "ovsdb-client", args...)
	if err != nil {
		return err
	}

	return nil
}

func ovnConvert(nbDB string, sbDB string) ([][]string, error) {
	commands := [][]string{}

	// Patch the Northbound records.
	args := []string{"dump", "-f", "csv", nbDB, "OVN_Northbound"}
	if strings.Contains(sbDB, "ssl:") {
		args = append(args, "-c", "/etc/ovn/cert_host")
		args = append(args, "-p", "/etc/ovn/key_host")
		args = append(args, "-C", "/etc/ovn/ovn-central.crt")
	}

	output, err := subprocess.RunCommand("ovsdb-client", args...)
	if err != nil {
		return nil, err
	}

	data, err := ovnParseDump(output)
	if err != nil {
		return nil, err
	}

	for table, records := range data {
		for _, record := range records {
			for k, v := range record {
				needsFixing, newValue, err := ovnCheckValue(table, k, v)
				if err != nil {
					return nil, err
				}

				if needsFixing {
					cmd := []string{"ovn-nbctl", "--db", nbDB}
					if strings.Contains(nbDB, "ssl:") {
						cmd = append(cmd, "-c", "/etc/ovn/cert_host")
						cmd = append(cmd, "-p", "/etc/ovn/key_host")
						cmd = append(cmd, "-C", "/etc/ovn/ovn-central.crt")
					}

					cmd = append(cmd, []string{"set", table, record["_uuid"], fmt.Sprintf("%s=%s", k, newValue)}...)
					commands = append(commands, cmd)
				}
			}
		}
	}

	// Patch the Southbound records.
	args = []string{"dump", "-f", "csv", sbDB, "OVN_Southbound"}
	if strings.Contains(sbDB, "ssl:") {
		args = append(args, "-c", "/etc/ovn/cert_host")
		args = append(args, "-p", "/etc/ovn/key_host")
		args = append(args, "-C", "/etc/ovn/ovn-central.crt")
	}

	output, err = subprocess.RunCommand("ovsdb-client", args...)
	if err != nil {
		return nil, err
	}

	data, err = ovnParseDump(output)
	if err != nil {
		return nil, err
	}

	for table, records := range data {
		for _, record := range records {
			for k, v := range record {
				needsFixing, newValue, err := ovnCheckValue(table, k, v)
				if err != nil {
					return nil, err
				}

				if needsFixing {
					cmd := []string{"ovn-sbctl", "--db", sbDB}
					if strings.Contains(sbDB, "ssl:") {
						cmd = append(cmd, "-c", "/etc/ovn/cert_host")
						cmd = append(cmd, "-p", "/etc/ovn/key_host")
						cmd = append(cmd, "-C", "/etc/ovn/ovn-central.crt")
					}

					cmd = append(cmd, []string{"set", table, record["_uuid"], fmt.Sprintf("%s=%s", k, newValue)}...)
					commands = append(commands, cmd)
				}
			}
		}
	}

	return commands, nil
}

func ovnCheckValue(table string, k string, v string) (bool, string, error) {
	if !strings.Contains(v, "lxd") {
		return false, "", nil
	}

	if table == "DNS" && k == "records" {
		return false, "", nil
	}

	if table == "Chassis" && k == "other_config" {
		return false, "", nil
	}

	if table == "Chassis" && k == "external_ids" {
		return false, "", nil
	}

	if table == "Logical_Flow" && k == "actions" {
		return false, "", nil
	}

	if table == "DHCP_Options" && k == "options" {
		return false, "", nil
	}

	if table == "Logical_Router_Port" && k == "ipv6_ra_configs" {
		return false, "", nil
	}

	newValue := strings.Replace(v, "lxd-net", "incus-net", -1)
	newValue = strings.Replace(newValue, "lxd_acl", "incus_acl", -1)
	newValue = strings.Replace(newValue, "lxd_location", "incus_location", -1)
	newValue = strings.Replace(newValue, "lxd_net", "incus_net", -1)
	newValue = strings.Replace(newValue, "lxd_port_group", "incus_port_group", -1)
	newValue = strings.Replace(newValue, "lxd_project_id", "incus_project_id", -1)
	newValue = strings.Replace(newValue, "lxd_switch", "incus_switch", -1)
	newValue = strings.Replace(newValue, "lxd_switch_port", "incus_switch_port", -1)

	if v == newValue {
		return true, "", fmt.Errorf("Couldn't convert value %q for key %q in table %q", v, k, table)
	}

	return true, newValue, nil
}

func ovnParseDump(data string) (map[string][]map[string]string, error) {
	output := map[string][]map[string]string{}

	tableName := ""
	fields := []string{}
	newTable := false
	for _, line := range strings.Split(data, "\n") {
		if line == "" {
			continue
		}

		if !strings.Contains(line, ",") && strings.HasSuffix(line, " table") {
			newTable = true
			tableName = strings.Split(line, " ")[0]
			output[tableName] = []map[string]string{}
			continue
		}

		if newTable {
			newTable = false

			var err error
			fields, err = csv.NewReader(strings.NewReader(line)).Read()
			if err != nil {
				return nil, err
			}

			continue
		}

		record := map[string]string{}

		entry, err := csv.NewReader(strings.NewReader(line)).Read()
		if err != nil {
			return nil, err
		}

		for k, v := range entry {
			record[fields[k]] = v
		}

		output[tableName] = append(output[tableName], record)
	}

	return output, nil
}
