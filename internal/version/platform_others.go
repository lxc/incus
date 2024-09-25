//go:build !linux

package version

func getPlatformVersionStrings() []string {
	return []string{}
}
