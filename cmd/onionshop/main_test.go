package main

import (
	"strings"
	"testing"
)

func TestResolveAddrLoopbackAlwaysAllowed(t *testing.T) {
	t.Setenv(netNeverEnv, "")
	for _, tc := range []struct{ in, want string }{
		{"", "127.0.0.1:8080"}, // falls back to defaultAddr
		{"127.0.0.1:9090", "127.0.0.1:9090"},
		{"localhost:8080", "localhost:8080"},
	} {
		got, err := resolveAddr(tc.in)
		if err != nil {
			t.Fatalf("resolveAddr(%q): unexpected error %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("resolveAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveAddrRefusesPublicBindWithoutContainerMarker(t *testing.T) {
	t.Setenv(netNeverEnv, "")
	for _, in := range []string{"0.0.0.0:8080", ":8080", "10.0.0.5:8080"} {
		if _, err := resolveAddr(in); err == nil {
			t.Errorf("resolveAddr(%q): expected refusal, got nil error", in)
		} else if !strings.Contains(err.Error(), "localhost only") {
			t.Errorf("resolveAddr(%q): error %q should cite the localhost-only policy", in, err)
		}
	}
}

func TestResolveAddrInvalidAddress(t *testing.T) {
	t.Setenv(netNeverEnv, "")
	if _, err := resolveAddr("127.0.0.1"); err == nil {
		t.Fatal("expected error for missing port")
	}
}

func TestResolveAddrContainerMarkerAllowsContainedBind(t *testing.T) {
	t.Setenv(netNeverEnv, "1")
	// Wildcard forms normalize to an explicit 0.0.0.0 bind inside the
	// contained namespace so nginx can reach the veth IP.
	for _, in := range []string{":8080", "0.0.0.0:8080"} {
		got, err := resolveAddr(in)
		if err != nil {
			t.Fatalf("resolveAddr(%q) with %s=1: unexpected error %v", in, netNeverEnv, err)
		}
		if got != "0.0.0.0:8080" {
			t.Errorf("resolveAddr(%q) = %q, want 0.0.0.0:8080", in, got)
		}
	}
	// An explicit interface address is passed through untouched.
	got, err := resolveAddr("172.28.0.3:8080")
	if err != nil || got != "172.28.0.3:8080" {
		t.Errorf("resolveAddr(explicit ip) = %q, %v; want passthrough", got, err)
	}
	// Loopback still works with the marker set.
	if got, err := resolveAddr("127.0.0.1:8080"); err != nil || got != "127.0.0.1:8080" {
		t.Errorf("loopback under container marker: got %q, %v", got, err)
	}
}

func TestAllowContainerBindTruthiness(t *testing.T) {
	for v, want := range map[string]bool{
		"":        false,
		"0":       false,
		"false":   false,
		"no":      false,
		"1":       true,
		"true":    true,
		"TRUE":    true,
		"yes":     true,
		"garbage": false,
	} {
		t.Setenv(netNeverEnv, v)
		if got := allowContainerBind(); got != want {
			t.Errorf("%s=%q: allowContainerBind() = %v, want %v", netNeverEnv, v, got, want)
		}
	}
}

// TestCaptchaKeyFromSessionHash locks the SESSION_HASH_KEY -> CAPTCHA HMAC key
// derivation contract (main.go): blank/whitespace-only secrets yield no key
// (fail-loud at boot), the derivation is deterministic, the result always
// meets the captcha service's 16-byte minimum, and it never equals the raw
// secret bytes (domain-separated SHA-256, so rotating one secret cannot leak
// into the other's key space).
func TestCaptchaKeyFromSessionHash(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		wantOK bool
	}{
		{"empty", "", false},
		{"spaces only", "   ", false},
		{"tabs and newlines only", "\t\n \r\n", false},
		{"normal secret", "s3cr3t-session-hash", true},
		{"padded secret trims to non-empty", "  padded  ", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := captchaKeyFromSessionHash(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				if key != nil {
					t.Errorf("expected nil key when ok=false, got %q", key)
				}
				return
			}
			if len(key) < 16 {
				t.Errorf("key length = %d, want >= 16 (captcha.NewCaptchaService minimum)", len(key))
			}
		})
	}

	// Determinism: same input, same key across calls.
	a, okA := captchaKeyFromSessionHash("deterministic-input")
	b, okB := captchaKeyFromSessionHash("deterministic-input")
	if !okA || !okB || string(a) != string(b) {
		t.Errorf("derivation not deterministic: %q vs %q", a, b)
	}

	// Differs from raw value: the derived key must not be the secret itself.
	raw := "raw-secret-value-0123456789"
	k, ok := captchaKeyFromSessionHash(raw)
	if !ok {
		t.Fatal("expected ok=true for non-blank secret")
	}
	if string(k) == raw {
		t.Error("derived key equals raw secret; domain separation missing")
	}

	// Different secrets produce different keys.
	k1, _ := captchaKeyFromSessionHash("secret-one")
	k2, _ := captchaKeyFromSessionHash("secret-two")
	if string(k1) == string(k2) {
		t.Error("distinct secrets produced identical captcha keys")
	}
}
