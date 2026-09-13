package main

import "testing"

func TestParseFrequency(t *testing.T) {
	tests := map[string]uint64{
		"80000000": 80_000_000,
		"80.0M":    80_000_000,
		"1000K":    1_000_000,
		"7.074M":   7_074_000,
	}
	for input, want := range tests {
		got, err := parseFrequency(input)
		if err != nil {
			t.Fatalf("parseFrequency(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseFrequency(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestParseFrequencyRejectsFractionalHz(t *testing.T) {
	if _, err := parseFrequency("1.0000001M"); err == nil {
		t.Fatal("expected fractional-Hz error")
	}
}
