package targetfs

import (
	"context"
	"testing"
)

func TestRemoteEmptyWorkspaceNeverUsesHostDefault(t *testing.T) {
	for _, test := range []struct{ name, connectionID, configured, want string }{
		{"SSH root", "ssh-test", "", "/"},
		{"SSH configured workspace", "ssh-test", "/srv/projects", "/srv/projects"},
		{"WSL root", "wsl:Ubuntu", "", "/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewResolver(ResolverOptions{DefaultWorkspace: `P:\DevApp\Astrorder\server`, HelperPath: "astrorder-helper", WSLHelperPath: "astrorder-helper", Store: resolverStore{connection: map[string]any{"settings": map[string]any{"host": "example.test", "workspace": test.configured}}}})
			result, err := resolver.Resolve(context.Background(), ResolveRequest{ConnectionID: test.connectionID})
			if err != nil {
				t.Fatal(err)
			}
			if result.Workspace != test.want {
				t.Fatalf("remote workspace=%q, want %q", result.Workspace, test.want)
			}
			remote := result.FileSystem.(*remoteFileSystem)
			if remote.workspace != test.want || len(remote.allowedRoots) != 1 || remote.allowedRoots[0] != test.want {
				t.Fatalf("remote scope=%+v", remote)
			}
		})
	}
}
