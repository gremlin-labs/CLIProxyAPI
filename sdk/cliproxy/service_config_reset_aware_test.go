package cliproxy

import (
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestResetAwareRoutingSelector(t *testing.T) {
	for _, input := range []string{"reset-aware", "ResetAware", " soonest-reset "} {
		state := normalizedRoutingRuntimeState(&internalconfig.Config{
			Routing: internalconfig.RoutingConfig{Strategy: input},
		})
		if state.strategy != "reset-aware" {
			t.Fatalf("strategy(%q) = %q, want reset-aware", input, state.strategy)
		}
		if _, ok := newRoutingSelector(state).(*coreauth.ResetAwareSelector); !ok {
			t.Fatalf("selector type = %T, want *auth.ResetAwareSelector", newRoutingSelector(state))
		}
	}

	state := normalizedRoutingRuntimeState(&internalconfig.Config{
		Routing: internalconfig.RoutingConfig{Strategy: "reset-aware", SessionAffinity: true},
	})
	if _, ok := newRoutingSelector(state).(*coreauth.SessionAffinitySelector); !ok {
		t.Fatalf("with session affinity, selector type = %T, want the affinity wrapper", newRoutingSelector(state))
	}
}
