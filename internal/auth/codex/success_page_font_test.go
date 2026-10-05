package codex

import (
	"strings"
	"testing"
)

func TestSuccessPageEmbedsInstrumentSans(t *testing.T) {
	page := (&OAuthServer{}).generateSuccessHTML(false, "https://example.com")
	if strings.Count(page, "@font-face{font-family:'Instrument Sans'") != 1 {
		t.Fatal("success page should embed the Instrument Sans font face exactly once")
	}
	if !strings.Contains(page, "font-family: 'Instrument Sans'") {
		t.Fatal("success page body does not use Instrument Sans")
	}
}
