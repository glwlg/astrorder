//go:build !windows

package process

func systemEnvironment() []string             { return nil }
func systemPath() string                      { return "" }
func expandEnvironment(env []string) []string { return env }
