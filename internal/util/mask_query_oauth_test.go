package util

import (
	"strings"
	"testing"
)

func TestMaskSensitiveQueryRedactsOAuthCallbackParams(t *testing.T) {
	raw := "code=91IAh6E26r1UTudlb0Vk4m5bD23F2ARnXxc5rV6YeLfD1I&state=abcdef123456789&code_verifier=verifier-value-123&scope=user%3Ainference"
	got := MaskSensitiveQuery(raw)
	for _, secret := range []string{"91IAh6E26r1UTudlb0Vk4m5bD23F2ARnXxc5rV6YeLfD1I", "abcdef123456789", "verifier-value-123"} {
		if strings.Contains(got, secret) {
			t.Fatalf("masked query %q still contains %q", got, secret)
		}
	}
	if !strings.Contains(got, "scope=user%3Ainference") {
		t.Fatalf("masked query %q changed a non-sensitive parameter", got)
	}
}
