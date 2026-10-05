package safemode

import (
	"strings"
	"testing"
)

func TestExampleAPIKeyWarningPageUsesEmbeddedFont(t *testing.T) {
	page := ExampleAPIKeyWarningPageHTML([]string{"your-api-key-1"}, "/management.html")
	if !strings.Contains(page, "@font-face{font-family:'Instrument Sans'") {
		t.Fatal("warning page does not embed the Instrument Sans font face")
	}
	if !strings.Contains(page, "font-family:'Instrument Sans'") {
		t.Fatal("warning page body does not use the Instrument Sans font stack")
	}
	if strings.Contains(page, "http://") || strings.Contains(page, "https://fonts.") {
		t.Fatal("warning page must not load external font resources")
	}
}
