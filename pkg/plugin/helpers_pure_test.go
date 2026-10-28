package plugin

import (
	"testing"
)

func TestUTF16StringRoundTrip(t *testing.T) {
	buffer := make([]uint16, 16)

	copyStringToUTF16("Hello Ω", buffer, len(buffer))

	if got := stringFromUTF16(buffer); got != "Hello Ω" {
		t.Fatalf("stringFromUTF16() = %q, want %q", got, "Hello Ω")
	}
}

func TestUTF16StringTruncation(t *testing.T) {
	buffer := make([]uint16, 4)

	copyStringToUTF16("abcdef", buffer, len(buffer))

	if got := stringFromUTF16(buffer); got != "abc" {
		t.Fatalf("stringFromUTF16() = %q, want %q", got, "abc")
	}
}

func TestUTF16StringEmpty(t *testing.T) {
	if got := stringFromUTF16(nil); got != "" {
		t.Fatalf("stringFromUTF16(nil) = %q, want empty string", got)
	}
}
