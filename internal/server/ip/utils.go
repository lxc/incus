package ip

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

// FamilyV4 represents IPv4 protocol family.
const FamilyV4 = "-4"

// FamilyV6 represents IPv6 protocol family.
const FamilyV6 = "-6"

// LinkInfo represents the IP link details.
type LinkInfo struct {
	InterfaceName    string `json:"ifname"`
	Link             string `json:"link"`
	Master           string `json:"master"`
	Address          string `json:"address"`
	TXQueueLength    uint32 `json:"txqlen"`
	MTU              uint32 `json:"mtu"`
	OperationalState string `json:"operstate"`
	Info             struct {
		Kind      string `json:"info_kind"`
		SlaveKind string `json:"info_slave_kind"`
		Data      struct {
			Protocol string `json:"protocol"`
			ID       int    `json:"id"`
		} `json:"info_data"`
	} `json:"linkinfo"`
}

// GetLinkInfoByName returns the detailed information for the given link.
func GetLinkInfoByName(name string) (LinkInfo, error) {
	ipPath, err := exec.LookPath("ip")
	if err != nil {
		return LinkInfo{}, fmt.Errorf("ip command not found")
	}

	cmd := exec.Command(ipPath, "-j", "-d", "link", "show", name)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return LinkInfo{}, err
	}

	defer func() { _ = stdout.Close() }()

	err = cmd.Start()
	if err != nil {
		return LinkInfo{}, err
	}

	defer func() { _ = cmd.Wait() }()

	// Struct to decode ip output into.
	var linkInfoJSON []LinkInfo

	// Decode JSON output.
	dec := json.NewDecoder(stdout)
	err = dec.Decode(&linkInfoJSON)
	if err != nil && err != io.EOF {
		return LinkInfo{}, err
	}

	err = cmd.Wait()
	if err != nil {
		return LinkInfo{}, fmt.Errorf("no matching link found")
	}

	if len(linkInfoJSON) == 0 {
		return LinkInfo{}, fmt.Errorf("no matching link found")
	}

	return linkInfoJSON[0], nil
}
