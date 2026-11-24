package server

import "testing"

func TestParseDecimalUnits(t *testing.T) {
	cases := []struct{ in string; d int; want int64; bad bool }{
		{"0.733333", 12, 733333000000, false},
		{"1", 12, 1000000000000, false},
		{"110.00", 2, 11000, false},
		{"0,01", 2, 1, false},
		{"0.000000000001", 12, 1, false},
		{"0.0000000000001", 12, 0, true},
		{"-1", 2, 0, true},
		{"abc", 2, 0, true},
		{"0", 2, 0, true},
		{"99999999999999999999", 12, 0, true},
	}
	for _, c := range cases {
		got, err := parseDecimalUnits(c.in, c.d)
		if c.bad { if err == nil { t.Fatalf("%q: want error, got %d", c.in, got) }; continue }
		if err != nil || got != c.want { t.Fatalf("%q: got %d err %v want %d", c.in, got, err, c.want) }
	}
}

func TestParseDecimalUnitsAllowZero(t *testing.T) {
	for _, in := range []string{"0", "0.00", "0,00"} {
		got, err := parseDecimalUnitsAllowZero(in, 2)
		if err != nil || got != 0 { t.Fatalf("%q: got %d err %v", in, got, err) }
	}
	if got, err := parseDecimalUnitsAllowZero("10.00", 2); err != nil || got != 1000 {
		t.Fatalf("10.00: got %d err %v", got, err)
	}
}
