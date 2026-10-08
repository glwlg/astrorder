//go:build !windows

package process

func systemEnvironment() []string             { return nil }
func expandEnvironment(env []string) []string { return env }
