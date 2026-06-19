package execframe

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestEncodeDecode_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		typ  Type
		data []byte
	}{
		{"stdin", Stdin, []byte("ls -la\n")},
		{"stdout", Stdout, []byte("total 0\n")},
		{"empty", Stdout, []byte{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Encode(&buf, tc.typ, tc.data); err != nil {
				t.Fatalf("encode: %v", err)
			}
			gotT, gotD, err := Decode(&buf)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if gotT != tc.typ || !bytes.Equal(gotD, tc.data) {
				t.Fatalf("got (%v,%q), want (%v,%q)", gotT, gotD, tc.typ, tc.data)
			}
		})
	}
}

func TestDecode_BackToBack(t *testing.T) {
	var buf bytes.Buffer
	_ = Encode(&buf, Stdout, []byte("a"))
	_ = Encode(&buf, Stderr, []byte("b"))
	t1, d1, _ := Decode(&buf)
	t2, d2, _ := Decode(&buf)
	if t1 != Stdout || string(d1) != "a" || t2 != Stderr || string(d2) != "b" {
		t.Fatalf("back-to-back decode wrong: %v %q %v %q", t1, d1, t2, d2)
	}
}

func TestDecode_TruncatedAndOversized(t *testing.T) {
	if _, _, err := Decode(strings.NewReader("\x10\x00\x00")); err == nil {
		t.Fatal("expected error on truncated header")
	}
	// header claims 8 bytes, only 2 present
	if _, _, err := Decode(bytes.NewReader([]byte{0x10, 0, 0, 0, 8, 'h', 'i'})); err == nil {
		t.Fatal("expected error on truncated payload")
	}
	// oversized payload length rejected without allocating
	big := []byte{0x10, 0xFF, 0xFF, 0xFF, 0xFF}
	if _, _, err := Decode(bytes.NewReader(big)); err == nil {
		t.Fatal("expected error on oversized payload")
	}
	if err := Encode(io.Discard, Stdin, make([]byte, MaxPayload+1)); err == nil {
		t.Fatal("expected encode error on oversized payload")
	}
}

func TestResizeAndExitCodecs(t *testing.T) {
	r, c, err := DecodeResize(EncodeResize(40, 120))
	if err != nil || r != 40 || c != 120 {
		t.Fatalf("resize round-trip: %d %d %v", r, c, err)
	}
	code, msg, err := DecodeExit(EncodeExit(137, "killed"))
	if err != nil || code != 137 || msg != "killed" {
		t.Fatalf("exit round-trip: %d %q %v", code, msg, err)
	}
	if _, _, err := DecodeResize([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error on bad resize payload")
	}
}
