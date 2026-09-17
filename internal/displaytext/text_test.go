package displaytext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLineRemovesTerminalCommandsAndRecordSeparators(t *testing.T) {
	raw := "one\n2\tlabel\r\x00\a\x1b]52;c;SECRET\a\x1bPSECRET\x1b\\\x1b[2J\x1b[8mhidden\u202e三🙂é"
	got := Line(raw)
	for _, forbidden := range []string{"\n", "\t", "\r", "\x00", "\a", "SECRET", "\x1b[2J", "\x1b[8m", "\u202e"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unsafe %q in %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "三🙂é") || !utf8.ValidString(got) {
		t.Fatalf("damaged Unicode: %q", got)
	}
}

func TestPassiveColorsAndMultilineReset(t *testing.T) {
	raw := "\x1b[1;31mred\x1b[0m\n\x1b[38;5;208morange\x1b[38;2;10;20;30mrgb"
	got := Block(raw)
	for _, want := range []string{"\x1b[1;31m", "\x1b[0m\n", "\x1b[38;5;208m", "\x1b[38;2;10;20;30m"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q in %q", want, got)
		}
	}
}

func FuzzLineFraming(f *testing.F) {
	for _, seed := range []string{"hello", "\x1b]52;c;secret\a", "\x9b31mhi", "\u202e", "\x1b[31\nm", "\n1\tforged"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := Line(raw)
		if !utf8.ValidString(got) || strings.ContainsAny(got, "\n\r\t\x00") {
			t.Fatalf("unsafe line: %q", got)
		}
	})
}
