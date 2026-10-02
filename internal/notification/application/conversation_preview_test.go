package application

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncatePreviewNeverSplitsARune(t *testing.T) {
	text := strings.Repeat("a", 136) + "é" + strings.Repeat("b", 20)

	got := truncatePreview(text)

	if !utf8.ValidString(got) {
		t.Fatalf("preview is not valid UTF-8: %q", got)
	}
	if want := strings.Repeat("a", 136) + "..."; got != want {
		t.Fatalf("expected the cut before the split rune, got %q", got)
	}
}

func TestTruncatePreviewKeepsShortAndAsciiText(t *testing.T) {
	if got := truncatePreview("  hello "); got != "hello" {
		t.Fatalf("expected trimmed text, got %q", got)
	}
	long := strings.Repeat("x", 200)
	if got := truncatePreview(long); got != strings.Repeat("x", 137)+"..." {
		t.Fatalf("unexpected ascii cut %q", got)
	}
}
