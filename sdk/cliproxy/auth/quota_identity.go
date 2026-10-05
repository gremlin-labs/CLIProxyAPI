package auth

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// quotaIdentityKeys are the metadata fields that name the upstream account (and,
// for Claude, the organization) whose quota a credential draws on.
var quotaIdentityKeys = []string{"account_id", "chatgpt_account_id", "account_uuid", "organization_uuid", "email"}

// quotaAccountIdentity identifies the account behind a credential for passive quota
// observations. It is empty when the credential carries no account metadata.
func quotaAccountIdentity(a *Auth) string {
	if a == nil {
		return ""
	}
	fields := make([]string, 0, len(quotaIdentityKeys)+1)
	empty := true
	for _, key := range quotaIdentityKeys {
		value := strings.TrimSpace(authMetadataString(a, key))
		if value == "" {
			value = strings.TrimSpace(authAttribute(a, key))
		}
		if value != "" {
			empty = false
		}
		fields = append(fields, strings.ToLower(value))
	}
	if empty {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(a.Provider)) + "\x00" + strings.Join(fields, "\x00")
}

// quotaIdentityChanged reports whether a credential ID now names a different
// account, so quota observed for the old account must not steer routing.
func quotaIdentityChanged(existing, incoming *Auth) bool {
	if existing == nil || incoming == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(existing.Provider), strings.TrimSpace(incoming.Provider)) {
		return true
	}
	before, after := quotaAccountIdentity(existing), quotaAccountIdentity(incoming)
	return before != "" && after != "" && before != after
}

// clearPassiveQuotaObservations drops observed quota headers from the credential
// and its model states. Cooldowns are left alone.
func clearPassiveQuotaObservations(auth *Auth) {
	if auth == nil {
		return
	}
	auth.Quota.ClearObservationSignals()
	for model, state := range auth.ModelStates {
		if state == nil || (len(state.Quota.Signals) == 0 && state.Quota.ObservedAt.IsZero()) {
			continue
		}
		state = state.Clone()
		state.Quota.ClearObservationSignals()
		auth.ModelStates[model] = state
	}
}

type quotaObservationIdentityKey struct{}

// withQuotaObservationIdentity records which account a request was sent to, so its
// response headers are not applied if the credential is re-pointed mid-flight.
func withQuotaObservationIdentity(ctx context.Context, auth *Auth) context.Context {
	if ctx == nil || auth == nil {
		return ctx
	}
	identity := quotaAccountIdentity(auth)
	if identity == "" {
		return ctx
	}
	return context.WithValue(ctx, quotaObservationIdentityKey{}, identity)
}

// quotaObservationMatches reports whether response headers gathered under ctx still
// describe the account behind current.
func quotaObservationMatches(ctx context.Context, current *Auth) bool {
	if ctx == nil {
		return true
	}
	identity, _ := ctx.Value(quotaObservationIdentityKey{}).(string)
	if identity == "" {
		return true
	}
	return quotaAccountIdentity(current) == identity
}

// observeQuotaHeaders applies response headers unless they belong to another account.
func observeQuotaHeaders(ctx context.Context, auth *Auth, modelState *ModelState, provider string, headers http.Header, now time.Time) {
	if !quotaObservationMatches(ctx, auth) {
		logEntryWithRequestID(ctx).WithField("auth_id", auth.ID).Debug("quota observation skipped: credential now names a different account")
		return
	}
	auth.Quota.ObserveResponseHeadersForProvider(provider, headers, now)
	if modelState != nil {
		modelState.Quota.ObserveResponseHeadersForProvider(provider, headers, now)
	}
}
