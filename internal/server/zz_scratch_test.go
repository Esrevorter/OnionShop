package server

import (
	"log"
	"net/url"
	"os"
	"testing"
)

func TestZZScratchPGPPostTwice(t *testing.T) {
	// Diagnostic: always dump captured warn log, even on pass.
	var buf syncBuffer
	origWarnOnly := buf
	_ = origWarnOnly
	log.SetOutput(&buf)
	defer func() {
		log.SetOutput(os.Stderr)
		t.Logf("captured log:\n%s", buf.String())
	}()
	store := newFakeAuthStore(t)
	password, _ := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
	})
	if rec.Code != 303 {
		t.Fatalf("login POST = %d, want 303 (body %q)", rec.Code, truncate(rec.Body.String()))
	}

	pubArmor, _ := generateArmoredKeyPair(t)

	// First attempt: exactly what pgp_test.go does.
	rec1, formBody := postForm(t, app, "/account", c, "/account/pgp", url.Values{
		"pgp_public_key": {pubArmor},
	})
	t.Logf("FIRST POST code=%d body=%.200s", rec1.Code, truncate(rec1.Body.String()))
	_ = formBody

	// Second attempt: fresh GET /account to mine captcha, then manual POST so we
	// can see whether a plain retry behaves differently.
	recGet, body2 := c.get(t, "/account")
	t.Logf("GET /account code=%d len=%d", recGet.Code, len(body2))
	csrf := extractInput(t, body2, "csrf_token")
	capID := extractInput(t, body2, "captcha_id")
	capTok := extractInput(t, body2, "captcha_token")
	prompt := extractText(t, body2, "CAPTCHA:")
	sum := parsePromptSum(t, prompt)
	form := url.Values{}
	form.Set("csrf_token", csrf)
	form.Set("captcha_id", capID)
	form.Set("captcha_token", capTok)
	form.Set("captcha_answer", sum)
	form.Set("pgp_public_key", pubArmor)
	rec2 := c.postForm(t, "/account/pgp", form)
	t.Logf("SECOND POST code=%d body=%.200s", rec2.Code, truncate(rec2.Body.String()))
}
