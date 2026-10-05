// Package webfont embeds the Instrument Sans variable font (SIL Open Font License 1.1,
// see fonts/OFL.txt) for the HTML pages the server renders itself, such as OAuth
// callback and setup warning pages. The font is inlined as a data URI so the pages
// stay self-contained and never request external resources.
package webfont

import (
	_ "embed"
	"encoding/base64"
	"strings"
)

//go:embed fonts/InstrumentSans-Variable.woff2
var instrumentSansVariable []byte

// FontStack is the CSS font-family value for server-rendered pages.
const FontStack = `'Instrument Sans', ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif`

var fontFaceCSS = `@font-face{font-family:'Instrument Sans';src:url(data:font/woff2;base64,` +
	base64.StdEncoding.EncodeToString(instrumentSansVariable) +
	`) format('woff2');font-weight:400 700;font-stretch:75% 100%;font-style:normal;font-display:swap}`

// FontFaceCSS returns the @font-face rule declaring Instrument Sans.
func FontFaceCSS() string {
	return fontFaceCSS
}

// InjectHead inserts the @font-face rule before the first </head> of an HTML page.
// Pages without a </head> are returned unchanged.
func InjectHead(html string) string {
	return strings.Replace(html, "</head>", "<style>"+fontFaceCSS+"</style></head>", 1)
}
