package managementasset

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestDefaultPanelSourceIsGremlinLabsFork(t *testing.T) {
	const want = "https://api.github.com/repos/gremlin-labs/Cli-Proxy-API-Management-Center/releases/latest"
	if got := resolveReleaseURL(""); got != want {
		t.Fatalf("resolveReleaseURL(\"\") = %q, want %q", got, want)
	}
	if got := resolveReleaseURL(config.DefaultPanelGitHubRepository); got != want {
		t.Fatalf("resolveReleaseURL(default repository) = %q, want %q", got, want)
	}
}
