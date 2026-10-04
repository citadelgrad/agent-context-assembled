package render

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SafeText makes untrusted text inert in a terminal. Newlines, tabs and printable
// Unicode retain their layout; other controls and invalid UTF-8 bytes are shown
// as hexadecimal escapes. Apply only at text presentation, never to JSON data.
func SafeText(s string) string {
	var out strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&out, "\\x%02x", s[0])
		case unicode.IsControl(r) && r != '\n' && r != '\t':
			fmt.Fprintf(&out, "\\x%02x", r)
		default:
			out.WriteString(s[:size])
		}
		s = s[size:]
	}
	return out.String()
}
