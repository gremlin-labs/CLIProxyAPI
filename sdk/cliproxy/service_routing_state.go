package cliproxy

import (
	"context"
	"path/filepath"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	// routingStateFileName avoids the .json suffix so the auth directory watcher
	// never mistakes it for a credential file.
	routingStateFileName     = "routing-state.cpa"
	routingStateSaveInterval = time.Minute
)

// routingStatePath is where quota snapshots and session bindings persist, next to
// the auth files. It is empty in Home mode, where routing is coordinated remotely.
func (s *Service) routingStatePath() string {
	if s == nil || s.coreManager == nil || s.cfg == nil || s.cfg.Home.Enabled {
		return ""
	}
	authDir, errResolve := resolveCooldownStateAuthDir(s.cfg)
	if errResolve != nil || authDir == "" {
		return ""
	}
	return filepath.Join(authDir, routingStateFileName)
}

// restoreRoutingState reloads quota snapshots and session bindings saved by a
// previous run, so a restart neither forgets reset times nor scatters threads.
func (s *Service) restoreRoutingState() {
	path := s.routingStatePath()
	if path == "" {
		return
	}
	state, errLoad := coreauth.LoadRoutingStateFile(path)
	if errLoad != nil {
		log.Warnf("failed to load routing state: %v", errLoad)
		return
	}
	quotas, pins := s.coreManager.RestoreRoutingState(state, time.Now())
	if quotas > 0 || pins > 0 {
		log.Infof("restored routing state: %d quota snapshots, %d session bindings", quotas, pins)
	}
}

func (s *Service) saveRoutingState() {
	path := s.routingStatePath()
	if path == "" {
		return
	}
	if errSave := coreauth.SaveRoutingStateFile(path, s.coreManager.ExportRoutingState(time.Now())); errSave != nil {
		log.Warnf("failed to save routing state: %v", errSave)
	}
}

// startRoutingStatePersistence saves routing state periodically until ctx ends.
// Shutdown performs a final save.
func (s *Service) startRoutingStatePersistence(ctx context.Context) {
	if s.routingStatePath() == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(routingStateSaveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.saveRoutingState()
			}
		}
	}()
}
