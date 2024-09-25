package main

import (
	"fmt"
	"strings"

	liblxc "github.com/lxc/go-lxc"
)

func networkGet(container *liblxc.Container, index int, configKey string) map[string]string {
	keys := container.ConfigKeys(fmt.Sprintf("%s.%d", configKey, index))
	if len(keys) == 0 {
		return nil
	}

	dev := make(map[string]string)
	for _, k := range keys {
		value := container.ConfigItem(fmt.Sprintf("%s.%d.%s", configKey, index, k))
		if len(value) == 0 || strings.TrimSpace(value[0]) == "" {
			continue
		}

		dev[k] = value[0]
	}

	if len(dev) == 0 {
		return nil
	}

	return dev
}
