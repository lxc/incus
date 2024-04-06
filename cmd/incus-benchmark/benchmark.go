package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	config "github.com/lxc/incus/v6/shared/cliconfig"
)

const userConfigKey = "user.incus-benchmark"

// PrintServerInfo prints out information about the server.
func PrintServerInfo(c incus.InstanceServer) error {
	server, _, err := c.GetServer()
	if err != nil {
		return err
	}

	env := server.Environment
	fmt.Println("Test environment:")
	fmt.Println("  Server backend:", env.Server)
	fmt.Println("  Server version:", env.ServerVersion)
	fmt.Println("  Kernel:", env.Kernel)
	fmt.Println("  Kernel tecture:", env.KernelArchitecture)
	fmt.Println("  Kernel version:", env.KernelVersion)
	fmt.Println("  Storage backend:", env.Storage)
	fmt.Println("  Storage version:", env.StorageVersion)
	fmt.Println("  Container backend:", env.Driver)
	fmt.Println("  Container version:", env.DriverVersion)
	fmt.Println("")
	return nil
}

// LaunchContainers launches a set of containers.
func LaunchContainers(c incus.InstanceServer, count int, parallel int, image string, privileged bool, start bool, freeze bool) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	printTestConfig(count, batchSize, image, privileged, freeze)

	fingerprint, err := ensureImage(c, image)
	if err != nil {
		return duration, err
	}

	batchStart := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		name := getContainerName(count, index)

		err := createContainer(c, fingerprint, name, privileged)
		if err != nil {
			logf("Failed to launch container '%s': %s", name, err)
			return
		}

		if start {
			err := startContainer(c, name)
			if err != nil {
				logf("Failed to start container '%s': %s", name, err)
				return
			}

			if freeze {
				err := freezeContainer(c, name)
				if err != nil {
					logf("Failed to freeze container '%s': %s", name, err)
					return
				}
			}
		}
	}

	duration = processBatch(count, batchSize, batchStart)
	return duration, nil
}

// CreateContainers create the specified number of containers.
func CreateContainers(c incus.InstanceServer, count int, parallel int, fingerprint string, privileged bool) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	batchCreate := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		name := getContainerName(count, index)

		err := createContainer(c, fingerprint, name, privileged)
		if err != nil {
			logf("Failed to launch container '%s': %s", name, err)
			return
		}
	}

	duration = processBatch(count, batchSize, batchCreate)

	return duration, nil
}

// GetContainers returns containers created by the benchmark.
func GetContainers(c incus.InstanceServer) ([]api.Instance, error) {
	containers := []api.Instance{}

	allContainers, err := c.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return containers, err
	}

	for _, container := range allContainers {
		if container.Config[userConfigKey] == "true" {
			containers = append(containers, container)
		}
	}

	return containers, nil
}

// StartContainers starts containers created by the benchmark.
func StartContainers(c incus.InstanceServer, containers []api.Instance, parallel int) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	count := len(containers)
	logf("Starting %d containers", count)

	batchStart := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		container := containers[index]
		if !container.IsActive() {
			err := startContainer(c, container.Name)
			if err != nil {
				logf("Failed to start container '%s': %s", container.Name, err)
				return
			}
		}
	}

	duration = processBatch(count, batchSize, batchStart)
	return duration, nil
}

// StopContainers stops containers created by the benchmark.
func StopContainers(c incus.InstanceServer, containers []api.Instance, parallel int) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	count := len(containers)
	logf("Stopping %d containers", count)

	batchStop := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		container := containers[index]
		if container.IsActive() {
			err := stopContainer(c, container.Name)
			if err != nil {
				logf("Failed to stop container '%s': %s", container.Name, err)
				return
			}
		}
	}

	duration = processBatch(count, batchSize, batchStop)
	return duration, nil
}

// DeleteContainers removes containers created by the benchmark.
func DeleteContainers(c incus.InstanceServer, containers []api.Instance, parallel int) (time.Duration, error) {
	var duration time.Duration

	batchSize, err := getBatchSize(parallel)
	if err != nil {
		return duration, err
	}

	count := len(containers)
	logf("Deleting %d containers", count)

	batchDelete := func(index int, wg *sync.WaitGroup) {
		defer wg.Done()

		container := containers[index]
		name := container.Name
		if container.IsActive() {
			err := stopContainer(c, name)
			if err != nil {
				logf("Failed to stop container '%s': %s", name, err)
				return
			}
		}

		err = deleteContainer(c, name)
		if err != nil {
			logf("Failed to delete container: %s", name)
			return
		}
	}

	duration = processBatch(count, batchSize, batchDelete)
	return duration, nil
}

func ensureImage(c incus.InstanceServer, image string) (string, error) {
	var fingerprint string

	if strings.Contains(image, ":") {
		defaultConfig := config.NewConfig("", true)
		defaultConfig.UserAgent = version.UserAgent

		remote, fp, err := defaultConfig.ParseRemote(image)
		if err != nil {
			return "", err
		}

		fingerprint = fp

		imageServer, err := defaultConfig.GetImageServer(remote)
		if err != nil {
			return "", err
		}

		if fingerprint == "" {
			fingerprint = "default"
		}

		alias, _, err := imageServer.GetImageAlias(fingerprint)
		if err == nil {
			fingerprint = alias.Target
		}

		_, _, err = c.GetImage(fingerprint)
		if err != nil {
			logf("Importing image into local store: %s", fingerprint)
			image, _, err := imageServer.GetImage(fingerprint)
			if err != nil {
				logf("Failed to import image: %s", err)
				return "", err
			}

			err = copyImage(c, imageServer, *image)
			if err != nil {
				logf("Failed to import image: %s", err)
				return "", err
			}
		}
	} else {
		fingerprint = image
		alias, _, err := c.GetImageAlias(image)
		if err == nil {
			fingerprint = alias.Target
		} else {
			_, _, err = c.GetImage(image)
		}

		if err != nil {
			logf("Image not found in local store: %s", image)
			return "", err
		}
	}

	logf("Found image in local store: %s", fingerprint)
	return fingerprint, nil
}
