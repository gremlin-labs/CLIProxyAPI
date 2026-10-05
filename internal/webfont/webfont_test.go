package webfont

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmbeddedFontIsWOFF2(t *testing.T) {
	if !bytes.HasPrefix(instrumentSansVariable, []byte("wOF2")) {
		t.Fatalf("embedded font is not a WOFF2 file (len=%d)", len(instrumentSansVariable))
	}
	if !strings.Contains(FontFaceCSS(), "font-family:'Instrument Sans'") {
		t.Fatal("FontFaceCSS() does not declare Instrument Sans")
	}
	if !strings.HasPrefix(FontStack, "'Instrument Sans'") {
		t.Fatalf("FontStack = %q, want Instrument Sans first", FontStack)
	}
}

func TestInjectHeadAddsFontFaceOnce(t *testing.T) {
	page := "<html><head><title>x</title></head><body></head></body></html>"
	got := InjectHead(page)
	if strings.Count(got, "@font-face") != 1 {
		t.Fatalf("want exactly one @font-face, got %d", strings.Count(got, "@font-face"))
	}
	if !strings.Contains(got, "<style>@font-face") || !strings.Contains(got, "</style></head><body>") {
		t.Fatalf("font face not injected before the first </head>: %.200q", got)
	}
	if InjectHead("<p>no head</p>") != "<p>no head</p>" {
		t.Fatal("page without </head> should be unchanged")
	}
}
