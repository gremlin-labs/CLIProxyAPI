package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func stubInstallManagementHTML(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	previous := installManagementHTML
	installManagementHTML = func(context.Context, string, string, string) error {
		calls++
		return err
	}
	t.Cleanup(func() { installManagementHTML = previous })
	return &calls
}

func postManagementHTMLInstall(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v0/management/management-html/install", nil)
	request.Header.Set("Authorization", "Bearer test-management-key")
	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)
	return response
}

func TestInstallManagementHTMLIgnoresAutoUpdateOptOut(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	calls := stubInstallManagementHTML(t, nil)
	cfg := &proxyconfig.Config{}
	cfg.RemoteManagement.DisableAutoUpdatePanel = true
	server := newTestServerWithConfig(t, cfg)

	response := postManagementHTMLInstall(t, server)
	if response.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("status = %d calls = %d, want 200 and one install; body=%s", response.Code, *calls, response.Body.String())
	}
}

func TestInstallManagementHTMLReportsFailure(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	stubInstallManagementHTML(t, errors.New("management asset digest mismatch"))
	server := newTestServer(t)

	response := postManagementHTMLInstall(t, server)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadGateway, response.Body.String())
	}
}

func TestInstallManagementHTMLDisabledControlPanel(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	calls := stubInstallManagementHTML(t, nil)
	cfg := &proxyconfig.Config{}
	cfg.RemoteManagement.DisableControlPanel = true
	server := newTestServerWithConfig(t, cfg)

	response := postManagementHTMLInstall(t, server)
	if response.Code != http.StatusNotFound || *calls != 0 {
		t.Fatalf("status = %d calls = %d, want 404 and no install", response.Code, *calls)
	}
}
