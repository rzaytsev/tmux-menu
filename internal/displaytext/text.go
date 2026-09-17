// Package displaytext sanitizes text at terminal display boundaries.
package displaytext

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Line preserves passive colors but never record separators or terminal commands.
func Line(raw string) string {
	if len(raw) > 16<<10 {
		raw = raw[:16<<10]
	}
	return sanitize(raw, false)
}

// Block preserves line breaks for previews, resetting styles at each boundary.
func Block(raw string) string { return sanitize(raw, true) }

func UnsafeControl(r rune) bool {
	return unicode.IsControl(r) || r == 0x061c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

func sanitize(raw string, multiline bool) string {
	var out strings.Builder
	out.Grow(len(raw) + 8)
	out.WriteString("\x1b[0m")
	var state byte
	for len(raw) > 0 {
		previous := state
		seq, _, n, next := ansi.DecodeSequence(raw, state, nil)
		if n <= 0 {
			break
		}
		raw, state = raw[n:], next
		if passiveSGR(seq) {
			out.WriteString(seq)
			continue
		}
		if previous != ansi.NormalState || strings.HasPrefix(seq, "\x1b") || len(seq) > 0 && seq[0] >= 0x80 && seq[0] <= 0x9f {
			continue
		}
		for _, r := range seq {
			switch {
			case r == '\n' && multiline:
				out.WriteString("\x1b[0m\n")
			case r == '\n' || r == '\r' || r == '\t':
				out.WriteByte(' ')
			case !UnsafeControl(r):
				out.WriteRune(r)
			}
		}
	}
	out.WriteString("\x1b[0m")
	return out.String()
}

func passiveSGR(seq string) bool {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") || len(seq) > 128 {
		return false
	}
	fields := strings.Split(seq[2:len(seq)-1], ";")
	for i := 0; i < len(fields); i++ {
		if fields[i] == "" {
			continue
		}
		p, err := strconv.Atoi(fields[i])
		if err != nil {
			return false
		}
		switch {
		case p == 0 || p == 1 || p == 2 || p == 3 || p == 4 || p == 7 || p == 22 || p == 23 || p == 24 || p == 27 || p == 39 || p == 49,
			p >= 30 && p <= 37, p >= 40 && p <= 47, p >= 90 && p <= 107:
		case p == 38 || p == 48:
			if i+1 >= len(fields) {
				return false
			}
			count := 0
			switch fields[i+1] {
			case "5":
				count = 1
			case "2":
				count = 3
			default:
				return false
			}
			if i+1+count >= len(fields) {
				return false
			}
			for _, field := range fields[i+2 : i+2+count] {
				color, err := strconv.Atoi(field)
				if err != nil || color < 0 || color > 255 {
					return false
				}
			}
			i += 1 + count
		default:
			return false
		}
	}
	return true
}
