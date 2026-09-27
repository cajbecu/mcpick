package trust

import (
	"fmt"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// SafeJSON escapes, in encoded JSON, what encoding/json leaves raw but a
// terminal acts on or draws misleadingly: the C1 controls (U+0080–U+009F,
// one of which is a CSI) and the format characters (unicode.Cf: bidi
// overrides, zero-width joiners, tag characters), as \uXXXX — a surrogate
// pair above the BMP. Outside strings JSON is ASCII, so every such rune is
// inside one and the escape keeps the value the same. Everything --json
// and serve print goes through it.
func SafeJSON(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r >= 0x80 && (r <= 0x9f || unicode.Is(unicode.Cf, r)) {
			if r1, r2 := utf16.EncodeRune(r); r1 != utf8.RuneError {
				out = fmt.Appendf(out, `\u%04x\u%04x`, r1, r2)
			} else {
				out = fmt.Appendf(out, `\u%04x`, r)
			}
		} else {
			out = append(out, data[i:i+size]...)
		}
		i += size
	}
	return out
}
