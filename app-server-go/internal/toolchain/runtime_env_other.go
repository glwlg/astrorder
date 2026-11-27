//go:build !windows

package toolchain

func readSystemRegistryPaths() []string {
	return nil
}
