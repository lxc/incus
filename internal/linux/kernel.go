//go:build linux

package linux

import (
	"fmt"
	"reflect"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

// LoadModule loads the kernel module with the given name, by invoking
// modprobe. This respects any modprobe configuration on the system.
func LoadModule(module string) error {
	if util.PathExists(fmt.Sprintf("/sys/module/%s", module)) {
		return nil
	}

	_, err := subprocess.RunCommand("modprobe", "-b", module)
	return err
}

// Utsname returns the same info as unix.Utsname, as strings.
type Utsname struct {
	Sysname    string
	Nodename   string
	Release    string
	Version    string
	Machine    string
	Domainname string
}

// Uname returns Utsname as strings.
func Uname() (*Utsname, error) {
	/*
	 * Based on: https://groups.google.com/forum/#!topic/golang-nuts/Jel8Bb-YwX8
	 * there is really no better way to do this, which is
	 * unfortunate. Also, we ditch the more accepted CharsToString
	 * version in that thread, since it doesn't seem as portable,
	 * viz. github issue #206.
	 */

	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, err
	}

	return &Utsname{
		Sysname:    intArrayToString(uname.Sysname),
		Nodename:   intArrayToString(uname.Nodename),
		Release:    intArrayToString(uname.Release),
		Version:    intArrayToString(uname.Version),
		Machine:    intArrayToString(uname.Machine),
		Domainname: intArrayToString(uname.Domainname),
	}, nil
}

func intArrayToString(arr any) string {
	slice := reflect.ValueOf(arr)
	s := ""
	for i := 0; i < slice.Len(); i++ {
		val := slice.Index(i)
		valInt := int64(-1)

		switch val.Kind() {
		case reflect.Int:
		case reflect.Int8:
			valInt = int64(val.Int())
		case reflect.Uint:
		case reflect.Uint8:
			valInt = int64(val.Uint())
		default:
			continue
		}

		if valInt == 0 {
			break
		}

		s += string(byte(valInt))
	}

	return s
}
