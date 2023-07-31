package benchmark

import (
	"github.com/cyphar/incus/client"
	"github.com/cyphar/incus/shared/api"
)

func createContainer(c lxd.ContainerServer, fingerprint string, name string, privileged bool) error {
	config := map[string]string{}
	if privileged {
		config["security.privileged"] = "true"
	}

	config[userConfigKey] = "true"

	req := api.ContainersPost{
		Name: name,
		Source: api.ContainerSource{
			Type:        "image",
			Fingerprint: fingerprint,
		},
	}

	req.Config = config

	op, err := c.CreateContainer(req)
	if err != nil {
		return err
	}

	return op.Wait()
}

func startContainer(c lxd.ContainerServer, name string) error {
	op, err := c.UpdateContainerState(
		name, api.ContainerStatePut{Action: "start", Timeout: -1}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func stopContainer(c lxd.ContainerServer, name string) error {
	op, err := c.UpdateContainerState(
		name, api.ContainerStatePut{Action: "stop", Timeout: -1, Force: true}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func freezeContainer(c lxd.ContainerServer, name string) error {
	op, err := c.UpdateContainerState(
		name, api.ContainerStatePut{Action: "freeze", Timeout: -1}, "")
	if err != nil {
		return err
	}

	return op.Wait()
}

func deleteContainer(c lxd.ContainerServer, name string) error {
	op, err := c.DeleteContainer(name)
	if err != nil {
		return err
	}

	return op.Wait()
}

func copyImage(c lxd.ContainerServer, s lxd.ImageServer, image api.Image) error {
	op, err := c.CopyImage(s, image, nil)
	if err != nil {
		return err
	}

	return op.Wait()
}
