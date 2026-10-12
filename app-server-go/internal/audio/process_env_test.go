package audio

import (
	"strings"
	"testing"
)

func TestLocalModelEnvironmentDoesNotHideGPUsWithEmptyVisibility(t *testing.T) {
	t.Setenv("GGML_VK_VISIBLE_DEVICES", "")
	for _, entry := range sanitizedEnv() {
		if strings.EqualFold(entry, "GGML_VK_VISIBLE_DEVICES=") {
			t.Fatal("empty Vulkan visibility hides every GPU from the local model")
		}
	}
}

func TestLocalModelEnvironmentPreservesExplicitVisibility(t *testing.T) {
	t.Setenv("GGML_VK_VISIBLE_DEVICES", "0")
	for _, entry := range sanitizedEnv() {
		if entry == "GGML_VK_VISIBLE_DEVICES=0" {
			return
		}
	}
	t.Fatal("explicit Vulkan device selection was discarded")
}
