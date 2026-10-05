package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const routingStateVersion = 1

// RoutingState is the routing knowledge worth keeping across restarts: each
// credential's last passive quota snapshot (used by reset-aware routing) and the
// session affinity bindings (which keep threads on their prompt-cached credential).
type RoutingState struct {
	Version int                      `json:"version"`
	SavedAt time.Time                `json:"saved_at"`
	Quota   map[string]QuotaSnapshot `json:"quota,omitempty"`
	Pins    []SessionPin             `json:"pins,omitempty"`
}

// QuotaSnapshot is a credential's last observed quota headers.
type QuotaSnapshot struct {
	ObservedAt time.Time         `json:"observed_at"`
	Signals    map[string]string `json:"signals"`
	// Account identifies the upstream account the signals were observed for, so a
	// credential re-pointed at another account does not inherit them.
	Account string `json:"account,omitempty"`
}

// ExportRoutingState captures quota snapshots and session bindings.
func (m *Manager) ExportRoutingState(now time.Time) RoutingState {
	state := RoutingState{Version: routingStateVersion, SavedAt: now}
	if m == nil {
		return state
	}
	m.mu.RLock()
	selector := m.selector
	for id, auth := range m.auths {
		if auth == nil || len(auth.Quota.Signals) == 0 || auth.Quota.ObservedAt.IsZero() {
			continue
		}
		if state.Quota == nil {
			state.Quota = make(map[string]QuotaSnapshot)
		}
		signals := make(map[string]string, len(auth.Quota.Signals))
		for key, value := range auth.Quota.Signals {
			signals[key] = value
		}
		state.Quota[id] = QuotaSnapshot{ObservedAt: auth.Quota.ObservedAt, Signals: signals, Account: quotaAccountIdentity(auth)}
	}
	m.mu.RUnlock()
	if affinity, ok := selector.(*SessionAffinitySelector); ok && affinity.cache != nil {
		state.Pins = affinity.cache.Export(now)
	}
	return state
}

// RestoreRoutingState applies a saved state to registered credentials. A quota
// snapshot only replaces an older (or missing) one observed for the same account,
// and bindings are restored only for credentials that still exist. It returns the counts restored.
func (m *Manager) RestoreRoutingState(state RoutingState, now time.Time) (quotas, pins int) {
	if m == nil || state.Version != routingStateVersion {
		return 0, 0
	}
	m.mu.Lock()
	for id, snapshot := range state.Quota {
		auth, ok := m.auths[id]
		if !ok || auth == nil || len(snapshot.Signals) == 0 || snapshot.ObservedAt.IsZero() {
			continue
		}
		if !snapshot.ObservedAt.After(auth.Quota.ObservedAt) {
			continue
		}
		if account := quotaAccountIdentity(auth); snapshot.Account != "" && account != "" && snapshot.Account != account {
			continue
		}
		signals := make(map[string]string, len(snapshot.Signals))
		for key, value := range snapshot.Signals {
			signals[key] = value
		}
		auth.Quota.Signals = signals
		auth.Quota.ObservedAt = snapshot.ObservedAt
		quotas++
	}
	selector := m.selector
	m.mu.Unlock()

	if affinity, ok := selector.(*SessionAffinitySelector); ok && affinity.cache != nil {
		pins = affinity.cache.Restore(state.Pins, now, func(authID string) bool {
			_, exists := m.GetByID(authID)
			return exists
		})
	}
	return quotas, pins
}

// LoadRoutingStateFile reads a routing state file. A missing file is empty state.
func LoadRoutingStateFile(path string) (RoutingState, error) {
	data, errRead := os.ReadFile(path)
	if errRead != nil {
		if errors.Is(errRead, os.ErrNotExist) {
			return RoutingState{}, nil
		}
		return RoutingState{}, fmt.Errorf("read routing state: %w", errRead)
	}
	var state RoutingState
	if errUnmarshal := json.Unmarshal(data, &state); errUnmarshal != nil {
		return RoutingState{}, fmt.Errorf("parse routing state: %w", errUnmarshal)
	}
	return state, nil
}

// SaveRoutingStateFile atomically writes a routing state file with owner-only permissions.
func SaveRoutingStateFile(path string, state RoutingState) error {
	data, errMarshal := json.Marshal(state)
	if errMarshal != nil {
		return fmt.Errorf("encode routing state: %w", errMarshal)
	}
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0o700); errMkdir != nil {
		return fmt.Errorf("create routing state directory: %w", errMkdir)
	}
	tmp, errCreate := os.CreateTemp(filepath.Dir(path), ".routing-state-*.tmp")
	if errCreate != nil {
		return fmt.Errorf("create routing state temp file: %w", errCreate)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, errWrite := tmp.Write(data); errWrite != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write routing state: %w", errWrite)
	}
	if errClose := tmp.Close(); errClose != nil {
		cleanup()
		return fmt.Errorf("close routing state: %w", errClose)
	}
	if errChmod := os.Chmod(tmpName, 0o600); errChmod != nil {
		cleanup()
		return fmt.Errorf("set routing state permissions: %w", errChmod)
	}
	if errRename := os.Rename(tmpName, path); errRename != nil {
		cleanup()
		return fmt.Errorf("replace routing state: %w", errRename)
	}
	return nil
}
