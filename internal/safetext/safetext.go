// Package safetext is the one place that decides which runes untrusted
// durable text may not send to a terminal or a Markdown page as themselves,
// and how each is written instead. The bounded status views and the complete
// status page both render actor-controlled text; they share this so that a
// character neutralized on one page cannot come through on the other.
package safetext

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Hostile reports whether a rune must be shown as an escape rather than sent
// to a terminal as itself: every C0 and C1 control, DEL, every format
// character — the bidi overrides and isolates, and the zero-width marks that
// let one string print as another — and the line and paragraph separators.
// Newline, tab and carriage return are in that first class deliberately. A
// caller that renders text whole must not let it invent lines, because a line
// an attacker writes looks exactly like a line the program wrote.
func Hostile(value rune) bool {
	return value < 0x20 || value == 0x7f || (value >= 0x80 && value <= 0x9f) ||
		unicode.Is(unicode.Cf, value) || value == '\u2028' || value == '\u2029'
}

// Encode writes one rune as a visible escape. The widths are the conventional
// ones, and they are not decoration: \u is exactly four hex digits, so a rune
// above U+FFFF written that way would run to five and the next character could
// not be told from part of the escape.
func Encode(out *strings.Builder, value rune) {
	switch {
	case value <= 0xff:
		fmt.Fprintf(out, `\x%02x`, value)
	case value <= 0xffff:
		fmt.Fprintf(out, `\u%04x`, value)
	default:
		fmt.Fprintf(out, `\U%08x`, value)
	}
}

// Safe neutralizes user-controlled text a caller renders whole: every hostile
// rune and every byte that is not UTF-8 becomes a visible escape, with no
// one-line fold and no byte cap, so a long message keeps its length and its
// runs of spacing exactly. The durable bytes are untouched; this is what a
// page shows, not what the log holds.
//
// It escapes newline and tab as well. Keeping them would have preserved the
// shape of a message, but shape is exactly what an attacker wants: a newline
// lets untrusted text add a line to a rendered block, and a reader has no way
// to tell that line from one the program wrote.
func Safe(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); {
		decoded, size := utf8.DecodeRuneInString(value[index:])
		switch {
		case decoded == utf8.RuneError && size == 1:
			Encode(&out, rune(value[index]))
		case Hostile(decoded):
			Encode(&out, decoded)
		default:
			out.WriteString(value[index : index+size])
		}
		index += size
	}
	return out.String()
}
