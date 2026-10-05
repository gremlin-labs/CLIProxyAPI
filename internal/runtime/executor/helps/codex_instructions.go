package helps

import (
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/codexinstructions"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ApplyCodexConfiguredInstructions merges the configured private instructions into a
// Codex request body. It applies only when private instructions are enabled, the request
// handler marked the request private, the model is eligible, and the credential passes
// the OAuth-only and marked-credential checks. Callers must run it before the payload
// finalizer so user payload rules remain the final barrier.
func ApplyCodexConfiguredInstructions(cfg *config.Config, auth *cliproxyauth.Auth, model string, body []byte, meta map[string]any) []byte {
	if cfg == nil || len(body) == 0 {
		return body
	}
	settings := cfg.Codex.Instructions
	if !settings.Enabled || !codexinstructions.RequestIsPrivate(meta) {
		return body
	}
	if auth == nil {
		return body
	}
	if settings.OAuthOnlyEnabled() && !codexAuthIsOAuth(auth) {
		return body
	}
	if settings.RequireAuthAllowEnabled() && !codexinstructions.AuthAllows(auth.Attributes, auth.Metadata) {
		return body
	}
	if !codexinstructions.ModelMatches(settings.Models, model) {
		return body
	}
	private := strings.TrimSpace(settings.Content)
	if private == "" && strings.TrimSpace(settings.File) != "" {
		data, errRead := os.ReadFile(strings.TrimSpace(settings.File))
		if errRead != nil {
			log.WithError(errRead).Warn("codex instructions: failed to read instructions file")
			return body
		}
		private = strings.TrimSpace(string(data))
	}
	if private == "" {
		return body
	}

	current := gjson.GetBytes(body, "instructions").String()
	var merged string
	switch strings.ToLower(strings.TrimSpace(settings.Mode)) {
	case config.CodexInstructionsModeReplace:
		merged = private
	case config.CodexInstructionsModeAppend:
		merged = joinCodexInstructions(current, private)
	default:
		merged = joinCodexInstructions(private, current)
	}
	out, errSet := sjson.SetBytes(body, "instructions", merged)
	if errSet != nil {
		return body
	}
	return out
}

// codexAuthIsOAuth reports whether a Codex credential is an OAuth login rather than
// a codex-api-key entry from the configuration.
func codexAuthIsOAuth(auth *cliproxyauth.Auth) bool {
	if auth == nil {
		return false
	}
	if strings.TrimSpace(auth.Attributes["api_key"]) != "" {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(auth.Attributes["source"]), "config:")
}

func joinCodexInstructions(first, second string) string {
	first = strings.TrimSpace(first)
	second = strings.TrimSpace(second)
	switch {
	case first == "":
		return second
	case second == "":
		return first
	default:
		return first + "\n\n" + second
	}
}
