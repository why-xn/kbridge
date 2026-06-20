package version

import "testing"

func TestString(t *testing.T) {
	Version, Commit, Date = "v1.2.3", "abc1234", "2026-06-19T00:00:00Z"
	got := String()
	if got != "v1.2.3 (commit abc1234, built 2026-06-19T00:00:00Z)" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestDefaults(t *testing.T) {
	// Re-import fresh defaults are compile-time; just assert String is non-empty
	// with zero-ish values.
	Version, Commit, Date = "dev", "none", "unknown"
	if String() == "" {
		t.Fatal("String() empty")
	}
}
