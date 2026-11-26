package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Privileged actions are authenticated, role-gated, CSRF-protected and audited.
// A CAPTCHA there adds no anti-abuse value and creates a permanent failure
// surface: the visible challenge belongs to a DIFFERENT form on the page, so
// submitting the privileged form never carries captcha_* fields and the gate
// rejects every attempt no matter how correctly the user answers.
func TestPrivilegedHandlersDoNotRequireCaptcha(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`func \(a \*App\) (handleAdmin\w+)\(`)
	fr := regexp.MustCompile(`a\.requireFriction\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := string(b)
		for _, m := range fn.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			rest := src[m[0]:]
			body := rest
			if k := strings.Index(rest[1:], "\nfunc "); k >= 0 {
				body = rest[:k+1]
			}
			if fr.MatchString(body) {
				t.Errorf("%s (%s) gates a privileged action behind a CAPTCHA; use CSRF + honeypot + audit", name, f)
			}
		}
	}
}

func TestFrictionSensitivePurposes(t *testing.T) {
	for _, p := range []string{"login", "register", "login2fa", "account", "withdraw", "accountPurpose"} {
		if p == "accountPurpose" {
			continue
		}
		if !frictionSensitive(p) {
			t.Errorf("purpose %q must always challenge", p)
		}
	}
	for _, p := range []string{"product", "checkout", "message", "ticket", "dispute"} {
		if frictionSensitive(p) {
			t.Errorf("purpose %q should accept a session grant", p)
		}
	}
}
