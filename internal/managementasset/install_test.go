package managementasset

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newReleaseServer(t *testing.T, body []byte, digest string) *httptest.Server {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITSTORE_GIT_TOKEN", "")
	t.Setenv("GITSTORE_GIT_URL", "")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/asset" {
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":[{"name":"management.html","browser_download_url":"` + server.URL + `/asset","digest":"` + digest + `"}]}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSyncManagementHTMLWritesVerifiedAsset(t *testing.T) {
	body := []byte("<html>panel</html>")
	sum := sha256.Sum256(body)
	server := newReleaseServer(t, body, "sha256:"+hex.EncodeToString(sum[:]))
	dir := t.TempDir()

	if err := syncManagementHTML(t.Context(), dir, server.Client(), server.URL+"/release", true); err != nil {
		t.Fatalf("syncManagementHTML() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, managementAssetName))
	if err != nil || string(got) != string(body) {
		t.Fatalf("asset = %q, %v; want %q", got, err, body)
	}
}

func TestSyncManagementHTMLRejectsDigestMismatch(t *testing.T) {
	server := newReleaseServer(t, []byte("<html>tampered</html>"), "sha256:"+hex.EncodeToString(make([]byte, 32)))
	dir := t.TempDir()

	if err := syncManagementHTML(t.Context(), dir, server.Client(), server.URL+"/release", true); err == nil {
		t.Fatal("syncManagementHTML() error = nil, want digest mismatch")
	}
	if _, err := os.Stat(filepath.Join(dir, managementAssetName)); !os.IsNotExist(err) {
		t.Fatalf("asset written despite digest mismatch: %v", err)
	}
}

func TestSyncManagementHTMLExplicitInstallRequiresDigest(t *testing.T) {
	server := newReleaseServer(t, []byte("<html>panel</html>"), "")
	dir := t.TempDir()

	if err := syncManagementHTML(t.Context(), dir, server.Client(), server.URL+"/release", true); err == nil {
		t.Fatal("syncManagementHTML() error = nil, want missing digest error")
	}
	if _, err := os.Stat(filepath.Join(dir, managementAssetName)); !os.IsNotExist(err) {
		t.Fatalf("asset written without a published digest: %v", err)
	}
}
