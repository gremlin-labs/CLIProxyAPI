package usagestore

import (
	"fmt"
	"sync"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

var (
	runtimeMu     sync.Mutex
	runtimeStore  *Store
	runtimePlugin *Plugin
)

// Configure opens/replaces the global durable usage store and registers the plugin.
// path may be empty to use the default; relative paths resolve with ResolveStorePath
// against configFilePath. enabled controls whether events are written.
// Without an absolute path, WRITABLE_PATH or a config file there is no stable
// location for the database, so no store is opened and (nil, nil) is returned.
func Configure(path, configFilePath string, retentionDays int, enabled bool) (*Store, error) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	if !storePathAnchored(path, configFilePath) {
		log.Debug("usagestore: no config file or absolute usage-store-path; request monitoring store disabled")
		return nil, nil
	}

	if runtimePlugin == nil {
		runtimePlugin = NewPlugin(nil)
		coreusage.RegisterNamedPlugin("usagestore", runtimePlugin)
	}
	runtimePlugin.SetEnabled(enabled)

	resolved := ResolveStorePath(path, configFilePath)
	// Reuse existing store when path matches.
	if runtimeStore != nil && runtimeStore.Path() == resolved {
		runtimeStore.SetRetentionDays(retentionDays)
		runtimePlugin.SetStore(runtimeStore)
		return runtimeStore, nil
	}

	// Close previous store if path changes.
	if runtimeStore != nil {
		runtimePlugin.SetStore(nil)
		if errClose := runtimeStore.Close(); errClose != nil {
			log.Errorf("usagestore: failed to close %s: %v", runtimeStore.Path(), errClose)
		}
		runtimeStore = nil
	}

	store, err := Open(Options{Path: resolved, RetentionDays: retentionDays})
	if err != nil {
		return nil, fmt.Errorf("usagestore: open %s: %w", resolved, err)
	}
	runtimeStore = store
	runtimePlugin.SetStore(store)
	log.Infof("usagestore: opened %s (enabled=%v retention_days=%d)", store.Path(), enabled, retentionDays)
	return store, nil
}

// Current returns the active store, if any.
func Current() *Store {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	return runtimeStore
}

// SetEnabled toggles persistence on the runtime plugin.
func SetEnabled(enabled bool) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if runtimePlugin != nil {
		runtimePlugin.SetEnabled(enabled)
	}
}

// CloseRuntime closes the global store.
func CloseRuntime() {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if runtimePlugin != nil {
		runtimePlugin.SetStore(nil)
	}
	if runtimeStore != nil {
		if errClose := runtimeStore.Close(); errClose != nil {
			log.Errorf("usagestore: failed to close %s: %v", runtimeStore.Path(), errClose)
		}
		runtimeStore = nil
	}
}
