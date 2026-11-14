package server

import (
	"net/url"
	"strings"
	"testing"
)

// Phase 3 regression guard (AGENTS.md §3). The friction contract is the set of
// form FIELD NAMES plus the locally served challenge image — never the visible
// label text. A UI redesign may reword "Human verification" at will; it may not
// drop csrf_token, the CAPTCHA triple, or the honeypot.
//
// Motivation: the responsive-UI redesign renamed the prompt label and broke
// five full-chain tests that scraped it, while the CAPTCHA itself stayed fully
// intact. Prose scraping cannot tell a copy edit from a real regression; this
// assertion can.
func TestFrictionContractOnRenderedForms(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	password, _ := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)

	// Anonymous surfaces.
	for _, target := range []string{"/login", "/register"} {
		c := newHTTPClient(app)
		rec, body := c.get(t, target)
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d, want 200 (body %q)", target, rec.Code, truncate(body))
		}
		assertFrictionFields(t, target, body)
	}

	// The account surface sits behind the session gate: authenticate first.
	c := newHTTPClient(app)
	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
	})
	if rec.Code != 303 {
		t.Fatalf("login POST = %d, want 303 (body %q)", rec.Code, truncate(rec.Body.String()))
	}
	recAcc, body := c.get(t, "/account")
	if recAcc.Code != 200 {
		t.Fatalf("GET /account = %d, want 200 (body %q)", recAcc.Code, truncate(body))
	}
	assertFrictionFields(t, "/account", body)
}

// assertFrictionFields checks the structural contract on one rendered page.
// Every name here is read back by requireFriction / honeypotTriggered, so a
// missing field is a silent security downgrade, not a cosmetic change.
func assertFrictionFields(t *testing.T, target, body string) {
	t.Helper()
	for _, want := range []string{
		`name="csrf_token"`,
		`name="captcha_id"`,
		`name="captcha_token"`,
		`name="captcha_answer"`,
		`name="sta"`,
		`src="/captcha/`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: friction contract broken — missing %s (AGENTS.md §3)", target, want)
		}
	}
}
