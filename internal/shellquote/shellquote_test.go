package shellquote

import "testing"

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"":          "''",
		"plain":     "'plain'",
		"two words": "'two words'",
		"it's":      "'it'\\''s'",
	}
	for input, want := range cases {
		if got := Quote(input); got != want {
			t.Fatalf("Quote(%q) = %q, want %q", input, got, want)
		}
	}
}
