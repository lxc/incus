package main

import (
	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

func createContainer(c incus.InstanceServer, fingerprint string, name string, privileged bool) error {
	config := map[string]string{}
	if privileged {
		config["security.privileged"] = "true"
	}

	config[userConfigKey] = "true"

	req := api.InstancesPost{
		Name: name,
		Source: api.InstanceSource{
			Type:        "image",
			Fingerprint: fingerprint,
		},
	}

	req.Config = config

	op, err := c.CreateInstance(req)
	if err != nil {
		return err
	}

	return op.Wait()
}

func startContainer(c incus.InstanceServer, name string) error {
	op, err := c.UpdateInstanceState(
		name, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func stopContainer(c incus.InstanceServer, name string) error {
	op, err := c.UpdateInstanceState(
		name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func freezeContainer(c incus.InstanceServer, name string) error {
	op, err := c.UpdateInstanceState(
		name, api.InstanceStatePut{Action: "freeze", Timeout: -1}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func deleteContainer(c incus.InstanceServer, name string) error {
	op, err := c.DeleteInstance(name)
	if err != nil {
		return err
	}

	return op.Wait()
}

func copyImage(c incus.InstanceServer, s incus.ImageServer, image api.Image) error {
	op, err := c.CopyImage(s, image, nil)
	if err != nil {
		return err
	}

	return op.Wait()
}
