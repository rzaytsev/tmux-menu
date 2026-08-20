package agenthud

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeTerminalNeutralizesActiveControls(t *testing.T) {
	hostile := []byte("safe" +
		"\x1b]52;c;Y2xpcGJvYXJk\a" +
		"\x1b]0;owned\x1b\\" +
		"\x1b]8;;https://example.test\x1b\\link\x1b]8;;\x1b\\" +
		"\x1b[2J\x1b[3;4H\x1b[?25l" +
		"\x1bPtmux;passthrough\x1b\\" +
		"\x1b_payload\x1b\\" +
		"\x1b^private\x1b\\" +
		"\x1bXignored\x1b\\" +
		"\a\r\b\x7f" +
		"done")

	got := SanitizeTerminal(hostile, TerminalLimits{Width: 80, Height: 4, MaxInputBytes: 4096, MaxRetainedBytes: 4096})
	plain := got.Plain()
	if plain != "safelinkdone" {
		t.Fatalf("plain terminal = %q, want %q", plain, "safelinkdone")
	}
	assertPassiveANSIOnly(t, got.ANSI())
}

func TestSanitizeTerminalPreservesOnlyCanonicalPassiveSGR(t *testing.T) {
	raw := []byte("\x1b[1;31;44mhot\x1b[22;39m cool\x1b[0m")
	got := SanitizeTerminal(raw, TerminalLimits{Width: 80, Height: 2, MaxInputBytes: 1024, MaxRetainedBytes: 1024})
	if got.Plain() != "hot cool" {
		t.Fatalf("plain terminal = %q", got.Plain())
	}
	ansi := got.ANSI()
	if !strings.HasPrefix(ansi, "\x1b[0m") {
		t.Fatalf("cell does not start with a trusted reset: %q", ansi)
	}
	for _, want := range []string{"\x1b[1;31;44m", "hot", "\x1b[44m", " cool", "\x1b[0m"} {
		if !strings.Contains(ansi, want) {
			t.Fatalf("ANSI terminal %q does not contain %q", ansi, want)
		}
	}
	assertPassiveANSIOnly(t, ansi)
}

func TestSanitizeTerminalNormalizesMalformedUnicodeAndBounds(t *testing.T) {
	raw := []byte("old\n\x1b[31unterminated\n\x9b31mC1\n" +
		"ab\t界c\n" +
		"e\u0301xy\n" +
		"left\u202Eright\u2066isolate\u2069\n" +
		"bad\xffutf8")
	got := SanitizeTerminal(raw, TerminalLimits{Width: 4, Height: 4, MaxInputBytes: 4096, MaxRetainedBytes: 64})

	want := "ab  \ne\u0301xy\nleft\nbad�"
	if got.Plain() != want {
		t.Fatalf("plain terminal = %q, want %q", got.Plain(), want)
	}
	if got.LineCount() != 4 || got.RetainedBytes() > 64 || !utf8.ValidString(got.Plain()) {
		t.Fatalf("terminal bounds invalid: lines=%d bytes=%d valid=%v", got.LineCount(), got.RetainedBytes(), utf8.ValidString(got.Plain()))
	}
	assertPassiveANSIOnly(t, got.ANSI())
}

func TestSanitizeTerminalCapsInputBeforeRetainingContent(t *testing.T) {
	raw := []byte(strings.Repeat("x", 4097))
	got := SanitizeTerminal(raw, TerminalLimits{Width: 200, Height: 2, MaxInputBytes: 64, MaxRetainedBytes: 32})
	if got.RetainedBytes() > 32 || len(got.Plain()) > 32 {
		t.Fatalf("terminal retained %d bytes and %d plain bytes", got.RetainedBytes(), len(got.Plain()))
	}
}

func TestSanitizeTextMakesEveryLabelSourceOpaqueAndPassive(t *testing.T) {
	values := map[string]string{
		"provider": "codex\x1b]0;owned\a",
		"status":   "attention\x1b[2J",
		"session":  "work\nother",
		"thread":   "fix\u202Etxt",
		"workdir":  "/tmp\rhidden",
		"icon":     "!\x1b[31m",
	}
	for source, raw := range values {
		t.Run(source, func(t *testing.T) {
			got := SanitizeText(raw, 12)
			if got.Width() > 12 || !utf8.ValidString(got.Plain()) {
				t.Fatalf("safe text width=%d plain=%q", got.Width(), got.Plain())
			}
			assertPassiveANSIOnly(t, got.ANSI())
		})
	}
}

func assertPassiveANSIOnly(t *testing.T, value string) {
	t.Helper()
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == '\n' {
			continue
		}
		if b == 0x1b {
			end := strings.IndexByte(value[i:], 'm')
			if end < 0 {
				t.Fatalf("unterminated escape in %q", value)
			}
			sequence := value[i : i+end+1]
			if len(sequence) < 3 || sequence[1] != '[' {
				t.Fatalf("active escape survived in %q", value)
			}
			for _, char := range sequence[2 : len(sequence)-1] {
				if (char < '0' || char > '9') && char != ';' {
					t.Fatalf("non-SGR escape survived: %q", sequence)
				}
			}
			i += end
			continue
		}
		if b >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(value[i:])
			if r == utf8.RuneError && size == 1 {
				t.Fatalf("invalid UTF-8 survived in %q", value)
			}
			if r >= 0x80 && r <= 0x9f {
				t.Fatalf("C1 rune %#x survived in %q", r, value)
			}
			i += size - 1
			continue
		}
		if b < 0x20 || b == 0x7f {
			t.Fatalf("control byte %#x survived in %q", b, value)
		}
	}
}
