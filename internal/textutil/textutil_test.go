package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestTruncate_ASCIIPassthrough(t *testing.T) {
	assert.Equal(t, "hello", Truncate("hello", 10))
	assert.Equal(t, "hello", Truncate("hello", 5))
}

func TestTruncate_ASCIITruncation(t *testing.T) {
	got := Truncate("hello world", 5)
	assert.Equal(t, "hello...", got)
	assert.True(t, utf8.ValidString(got))
}

func TestTruncate_MultibyteAccented(t *testing.T) {
	s := "héllo wörld"
	// Byte length (13) differs from rune count (11) here because of é/ö;
	// byte-index truncation at max=5 would split a multibyte rune.
	got := Truncate(s, 5)
	assert.True(t, utf8.ValidString(got), "result must be valid UTF-8, got %q", got)
	runes := []rune(s)
	assert.Equal(t, string(runes[:5])+"...", got)
}

func TestTruncate_MultibyteEmoji(t *testing.T) {
	s := "hello 🎉🎊🎈 world"
	got := Truncate(s, 8)
	assert.True(t, utf8.ValidString(got), "result must be valid UTF-8, got %q", got)
	runes := []rune(s)
	assert.Equal(t, string(runes[:8])+"...", got)
}

func TestTruncate_EmojiExactByteSplitRegression(t *testing.T) {
	// A byte-index truncation at a length that lands mid-rune would produce
	// invalid UTF-8 / mojibake. Confirm Truncate never does this regardless
	// of where max falls relative to multibyte boundaries.
	s := strings.Repeat("🎉", 20) // each emoji is a 4-byte rune
	for max := 1; max < 20; max++ {
		got := Truncate(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("Truncate(%q, %d) produced invalid UTF-8: %q", s, max, got)
		}
	}
}

func TestTruncate_MaxLessThanOrEqualZero(t *testing.T) {
	assert.Equal(t, "hello", Truncate("hello", 0))
	assert.Equal(t, "hello", Truncate("hello", -1))
	assert.Equal(t, "", Truncate("", 0))
}

func TestTruncate_ExactLength(t *testing.T) {
	assert.Equal(t, "hello", Truncate("hello", 5))
}

func TestTruncate_EmptyString(t *testing.T) {
	assert.Equal(t, "", Truncate("", 10))
}
