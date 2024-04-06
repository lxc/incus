package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/util"
)

var checkedKeys = []string{
	"lxc.aa_allow_incomplete",
	"lxc.aa_profile",
	"lxc.apparmor.allow_incomplete",
	"lxc.apparmor.profile",
	"lxc.arch",
	"lxc.autodev",
	"lxc.cap.drop",
	"lxc.environment",
	"lxc.haltsignal",
	"lxc.id_map",
	"lxc.idmap",
	"lxc.include",
	"lxc.loglevel",
	"lxc.mount",
	"lxc.mount.auto",
	"lxc.mount.entry",
	"lxc.pts",
	"lxc.pty.max",
	"lxc.rebootsignal",
	"lxc.rootfs",
	"lxc.rootfs.backend",
	"lxc.rootfs.mount",
	"lxc.rootfs.path",
	"lxc.seccomp",
	"lxc.signal.halt",
	"lxc.signal.reboot",
	"lxc.signal.stop",
	"lxc.start.auto",
	"lxc.start.delay",
	"lxc.start.order",
	"lxc.stopsignal",
	"lxc.tty",
	"lxc.tty.max",
	"lxc.uts.name",
	"lxc.utsname",
	"incus.migrated",
}

func getUnsupportedKeys(config []string) []string {
	var out []string

	for _, a := range config {
		supported := false

		for _, b := range checkedKeys {
			if a == b {
				supported = true
				break
			}
		}

		if !supported {
			out = append(out, a)
		}
	}

	return out
}

func getConfig(config []string, key string) []string {
	// Return an array since keys can be specified more than once
	var out []string

	for _, c := range config {
		text := strings.TrimSpace(c)

		// Ignore empty lines and comments
		if len(text) == 0 || strings.HasPrefix(text, "#") {
			continue
		}

		line := strings.Split(text, "=")
		if len(line) != 2 {
			continue
		}

		k := strings.TrimSpace(line[0])
		v := strings.Trim(strings.TrimSpace(line[1]), "\"")

		if k == key && len(v) > 0 {
			out = append(out, v)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func getConfigKeys(config []string) []string {
	// Make sure we don't have duplicate keys
	m := make(map[string]bool)
	for _, c := range config {
		text := strings.TrimSpace(c)

		// Ignore empty lines and comments
		if len(text) == 0 || strings.HasPrefix(text, "#") {
			continue
		}

		line := strings.Split(text, "=")
		key := strings.TrimSpace(line[0])
		if strings.HasPrefix(key, "lxc.") {
			m[key] = true
		}
	}

	var out []string
	for k := range m {
		out = append(out, k)
	}

	return out
}

func parseConfig(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	var config []string

	// Parse config
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())

		// Ignore empty lines and comments
		if len(text) == 0 || strings.HasPrefix(text, "#") {
			continue
		}

		line := strings.Split(text, "=")
		if len(line) != 2 {
			continue
		}

		key := strings.TrimSpace(line[0])
		value := strings.TrimSpace(line[1])

		switch key {
		// Parse user-added includes
		case "lxc.include":
			// Ignore our own default configs
			if strings.HasPrefix(value, "/usr/share/lxc/config/") {
				continue
			}

			if util.PathExists(value) {
				if internalUtil.IsDir(value) {
					files, err := os.ReadDir(value)
					if err != nil {
						return nil, err
					}

					for _, file := range files {
						path := filepath.Join(value, file.Name())
						if !strings.HasSuffix(path, ".conf") {
							continue
						}

						config = append(config, path)
					}
				} else {
					c, err := parseConfig(value)
					if err != nil {
						return nil, err
					}

					config = append(config, c...)
				}

				continue
			}
		// Expand any fstab
		case "lxc.mount":
			if !util.PathExists(value) {
				fmt.Println("Container fstab file doesn't exist, skipping...")
				continue
			}

			file, err := os.Open(value)
			if err != nil {
				return nil, err
			}

			defer func() { _ = file.Close() }()

			sc := bufio.NewScanner(file)
			for sc.Scan() {
				text := strings.TrimSpace(sc.Text())

				if len(text) > 0 && !strings.HasPrefix(text, "#") {
					config = append(config, fmt.Sprintf("lxc.mount.entry = %s", text))
				}
			}

			continue

		default:
			config = append(config, text)
		}
	}

	return config, nil
}
