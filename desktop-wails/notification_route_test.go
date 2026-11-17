package main

import (
	"net/url"
	"testing"
)

func TestNotificationRoutesAreLocalAndBoundToInstallation(t *testing.T) {
	for _, path := range []string{"/groups/group-1", "/chat/thread?agent_id=codex", "/chat/thread?agent_id=ssh%3Adev"} {
		u := notificationScheme("state-one") + "://open?path=" + url.QueryEscape(path)
		got, err := notificationPathFromURL(u, "state-one")
		if err != nil || got != path {
			t.Fatalf("lost notification route: %q %v", got, err)
		}
		if _, err := notificationPathFromURL(u, "state-two"); err == nil {
			t.Fatal("another installation claimed the notification")
		}
	}
	for _, path := range []string{
		"https://evil.test/groups/g", "//evil.test/groups/g", "/groups/../settings",
		"/groups/%2Fother", "/groups/g?redirect=https://evil.test", "/groups/..",
		"/chat/t", "/chat/t?agent_id=a&redirect=evil", "/groups/g#other", "/groups/g\n",
	} {
		if _, err := validNotificationPath(path); err == nil {
			t.Fatalf("accepted unsafe notification route %q", path)
		}
	}
}
