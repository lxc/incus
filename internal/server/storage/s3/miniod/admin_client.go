package miniod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/lxc/incus/v6/shared/subprocess"
)

// AddServiceAccountResp is the response body of the add service account call.
type AddServiceAccountResp struct {
	AccessKey    string `json:"accessKey,omitempty"`
	SecretKey    string `json:"secretKey,omitempty"`
	SessionToken string `json:"sessionToken,omitempty"`
}

// InfoServiceAccountResp is the response body of the info service account call.
type InfoServiceAccountResp struct {
	AccountStatus string         `json:"accountStatus"`
	ParentUser    string         `json:"parentUser"`
	Policy        map[string]any `json:"policy"`
}

// AdminClient represents minio client.
type AdminClient struct {
	process    *Process
	alias      string
	binaryName string
	varPath    string
}

// configDir returns path to the configuration directory for mc command.
func (c *AdminClient) configDir() string {
	return filepath.Join(c.varPath, "mc", "config")
}

// policyDir returns path to the location with temporary policy files.
func (c *AdminClient) policyDir() string {
	return filepath.Join(c.varPath, "mc", "policy")
}

// runClientCommand runs 'mc' command.
func (c *AdminClient) runClientCommand(ctx context.Context, workDir string, extraArgs ...string) (string, error) {
	args := []string{"-C", c.configDir()}

	args = append(args, extraArgs...)

	cmd := exec.CommandContext(ctx, c.binaryName, args...)

	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return stdout.String(), subprocess.NewRunError(c.binaryName, args, err, &stdout, &stderr)
	}

	return stdout.String(), nil
}

// writePolicy writes policy data to the file.
func (c *AdminClient) writePolicy(policy []byte) error {
	err := os.MkdirAll(c.policyDir(), 0755)
	if err != nil {
		return err
	}

	policyPath := filepath.Join(c.policyDir(), c.alias)

	err = os.WriteFile(policyPath, policy, 0644)
	if err != nil {
		return err
	}

	return nil
}

// isMinIOClient checks whether "mc" is the MinIO client binary or another software.
func (c *AdminClient) isMinIOClient() bool {
	out, err := c.runClientCommand(context.TODO(), "", "--version")
	if err != nil {
		return false
	}

	lines := strings.Split(out, "\n")
	if len(lines) < 1 {
		return false
	}

	if !strings.Contains(lines[0], "mc version") {
		return false
	}

	return true
}

// ServiceStop stops the MinIO cluster.
func (c *AdminClient) ServiceStop(ctx context.Context) error {
	_, err := c.runClientCommand(ctx, "", "admin", "service", "stop", c.alias)
	if err != nil {
		return err
	}

	return nil
}

// AddAlias adds a new alias to configuration file.
func (c *AdminClient) AddAlias(ctx context.Context) error {
	_, err := c.runClientCommand(ctx, "", "alias", "set", c.alias, c.process.url.String(), c.process.username, c.process.password)
	if err != nil {
		return err
	}

	return nil
}

// RemoveAlias removes an alias from configuration file.
func (c *AdminClient) RemoveAlias(ctx context.Context) error {
	_, err := c.runClientCommand(ctx, "", "alias", "rm", c.alias)
	if err != nil {
		return err
	}

	return nil
}

// AddServiceAccount adds a new service account.
func (c *AdminClient) AddServiceAccount(ctx context.Context, account, accessKey, secretKey string, policy []byte) (*AddServiceAccountResp, error) {
	cmd := []string{
		"--json",
		"admin",
		"user",
		"svcacct",
		"add",
		c.alias,
		account,
	}

	if accessKey != "" {
		cmd = append(cmd, "--access-key", accessKey)
	}

	if secretKey != "" {
		cmd = append(cmd, "--secret-key", secretKey)
	}

	if len(policy) > 0 {
		policyPath := filepath.Join(c.policyDir(), c.alias)
		err := c.writePolicy(policy)
		if err != nil {
			return nil, err
		}

		defer os.Remove(policyPath)

		cmd = append(cmd, "--policy", policyPath)
	}

	out, err := c.runClientCommand(ctx, "", cmd...)
	if err != nil {
		return nil, err
	}

	var resp AddServiceAccountResp
	err = json.Unmarshal([]byte(out), &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// DeleteServiceAccount removes a service account.
func (c *AdminClient) DeleteServiceAccount(ctx context.Context, accessKey string) error {
	_, err := c.runClientCommand(ctx, "", "admin", "user", "svcacct", "rm", c.alias, accessKey)
	if err != nil {
		return err
	}

	return nil
}

// InfoServiceAccount returns a service account info.
func (c *AdminClient) InfoServiceAccount(ctx context.Context, accessKey string) (*InfoServiceAccountResp, error) {
	out, err := c.runClientCommand(ctx, "", "--json", "admin", "user", "svcacct", "info", c.alias, accessKey)
	if err != nil {
		return nil, err
	}

	var resp InfoServiceAccountResp
	err = json.Unmarshal([]byte(out), &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

// UpdateServiceAccount modifies an existing service account.
func (c *AdminClient) UpdateServiceAccount(ctx context.Context, account, secretKey string, policy []byte) error {
	cmd := []string{
		"admin",
		"user",
		"svcacct",
		"edit",
		c.alias,
		account,
	}

	if secretKey != "" {
		cmd = append(cmd, "--secret-key", secretKey)
	}

	if len(policy) > 0 {
		policyPath := filepath.Join(c.policyDir(), c.alias)
		err := c.writePolicy(policy)
		if err != nil {
			return err
		}

		defer os.Remove(policyPath)

		cmd = append(cmd, "--policy", policyPath)
	}

	_, err := c.runClientCommand(ctx, "", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// ExportIAM exports IAM data.
func (c *AdminClient) ExportIAM(ctx context.Context) ([]byte, error) {
	iamDir := filepath.Join(c.varPath, "mc", "iam", uuid.NewString())

	err := os.MkdirAll(iamDir, 0755)
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.RemoveAll(iamDir) }()

	_, err = c.runClientCommand(ctx, iamDir, "admin", "cluster", "iam", "export", c.alias)
	if err != nil {
		return nil, err
	}

	iamPath := filepath.Join(iamDir, fmt.Sprintf("%s-%s", c.alias, "iam-info.zip"))
	iamBytes, err := os.ReadFile(iamPath)
	if err != nil {
		return nil, err
	}

	return iamBytes, nil
}
