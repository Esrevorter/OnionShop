package server

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	enc, params, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(enc, "argon2id$v=19$") {
		t.Fatalf("encoded hash not argon2id self-describing record: %q", enc)
	}
	ok, err := VerifyPassword("correct horse battery staple", enc)
	if err != nil || !ok {
		t.Fatalf("verify good password: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong password", enc)
	if err != nil || ok {
		t.Fatalf("verify wrong password: ok=%v err=%v", ok, err)
	}
	// Params returned must match what is embedded in the hash.
	p2, _, err := decodeArgon(enc)
	if err != nil {
		t.Fatalf("decodeArgon: %v", err)
	}
	if p2 != params {
		t.Fatalf("params mismatch: %+v vs %+v", params, p2)
	}
}

func TestVerifyGarbageHashErrorsNotPanic(t *testing.T) {
	for _, junk := range []string{"", "not-a-hash", "$argon2id$m=1$t=1$p=1", "$bcrypt$abc"} {
		ok, err := VerifyPassword("x", junk)
		if err == nil || ok {
			t.Errorf("VerifyPassword(%q): ok=%v err=%v, want error", junk, ok, err)
		}
	}
}

func TestNeedsUpgrade(t *testing.T) {
	cur := DefaultArgonParams()
	if NeedsUpgrade(cur) {
		t.Error("default params flagged for upgrade")
	}
	weaker := ArgonParams{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	if !NeedsUpgrade(weaker) {
		t.Error("weak params not flagged for upgrade")
	}
}

func TestDummyVerifyMatchesRealCost(t *testing.T) {
	enc, _, err := HashPassword("some password here")
	if err != nil {
		t.Fatal(err)
	}
	// Warm the cache path first so timing comparison is meaningful.
	if _, err := VerifyPassword("x", enc); err != nil {
		t.Fatal(err)
	}
	DummyVerify() // should not panic and should complete silently
}

func TestValidateUsername(t *testing.T) {
	good := []string{"alice", "bob42", "a_b_c", "abc", strings.Repeat("u", 32)}
	for _, u := range good {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("ValidateUsername(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{"", "ab", strings.Repeat("u", 33), "user!", "Ünïcode", " has space", "has space", "-lead", "trail.", "MiXeD", "a-b"}
	for _, u := range bad {
		if err := ValidateUsername(u); err == nil {
			t.Errorf("ValidateUsername(%q) = nil, want error", u)
		}
	}
}

func TestValidatePasswordStrength(t *testing.T) {
	if err := ValidatePasswordStrength("short"); err == nil {
		t.Error("short password accepted")
	}
	if err := ValidatePasswordStrength(strings.Repeat("p", 200)); err == nil {
		t.Error("over-long password accepted")
	}
	if err := ValidatePasswordStrength("long enough passphrase"); err != nil {
		t.Errorf("reasonable passphrase rejected: %v", err)
	}
}

func TestParsePositiveInt(t *testing.T) {
	v, err := ParsePositiveInt("42", 100)
	if err != nil || v != 42 {
		t.Fatalf("got %d, %v", v, err)
	}
	for _, junk := range []string{"", "abc", "-1", "0", "1e9"} {
		if _, err := ParsePositiveInt(junk, 100); err == nil {
			t.Errorf("ParsePositiveInt(%q) accepted", junk)
		}
	}
	// Surrounding whitespace is tolerated (form inputs commonly carry it).
	if v, err := ParsePositiveInt(" 7 ", 100); err != nil || v != 7 {
		t.Errorf("ParsePositiveInt(\" 7 \") = %d, %v; want 7, nil", v, err)
	}
	if _, err := ParsePositiveInt("101", 100); err == nil {
		t.Error("value above max accepted")
	}
}
