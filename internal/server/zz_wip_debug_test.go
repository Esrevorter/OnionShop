package server

import (
	"log"
	"net/http"
	"fmt"
	"net/url"
	"os"

	"testing"
)

// TEMPORARY WIP DIAGNOSTIC — DELETE BEFORE COMMIT.
func TestZZWIPDebug500(t *testing.T) {
	// capture full log output verbatim
	log.SetOutput(os.Stdout)
	defer log.SetOutput(os.Stderr)

	store := newFakeAuthStore(t)
	password, uid := store.seedUser(t, "alice", "customer")
	t.Logf("seeded uid=%q parseRoundTrip=%v", uuidString(uid), func() bool { u, ok := parseUUID(uuidString(uid)); return ok && u == uid }())
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login POST = %d, want 303 (body %q)", rec.Code, truncate(rec.Body.String()))
	}

	pubArmor, _ := generateArmoredKeyPair(t)

	recGet, body := c.get(t, "/account")
	t.Logf("GET /account code=%d len=%d", recGet.Code, len(body))
	csrf := extractInput(t, body, "csrf_token")
	capID := extractInput(t, body, "captcha_id")
	capTok := extractInput(t, body, "captcha_token")
	prompt := extractText(t, body, "CAPTCHA:")
	sum := parsePromptSum(t, prompt)
	t.Logf("capID prefix=%q tokPrefix=%q prompt=%q", capID[:8], capTok[:8], prompt)

	form := url.Values{}
	form.Set("csrf_token", csrf)
	form.Set("captcha_id", capID)
	form.Set("captcha_token", capTok)
	form.Set("captcha_answer", sum)
	form.Set("pgp_public_key", pubArmor)
	rec2 := c.postForm(t, "/account/pgp", form)
	t.Logf("POST /account/pgp (valid key) code=%d body=%q", rec2.Code, truncate(rec2.Body.String()))

	// Probe A: garbage key -> expect 422 with rejection message if friction OK.
	form.Set("pgp_public_key", "not a pgp key at all")
	rec3 := c.postForm(t, "/account/pgp", form) // replayed captcha: burned => expect fail path 422 or challenge-fail 422
	t.Logf("POST garbage (replay token) code=%d body=%q", rec3.Code, truncate(rec3.Body.String()))

	// Fresh GET + solve, then submit garbage once.
	recGet2, body2 := c.get(t, "/account")
	t.Logf("GET /account #2 code=%d len=%d", recGet2.Code, len(body2))
	f2 := url.Values{}
	f2.Set("csrf_token", extractInput(t, body2, "csrf_token"))
	f2.Set("captcha_id", extractInput(t, body2, "captcha_id"))
	f2.Set("captcha_token", extractInput(t, body2, "captcha_token"))
	f2.Set("captcha_answer", parsePromptSum(t, extractText(t, body2, "CAPTCHA:")))
	f2.Set("pgp_public_key", "not a pgp key at all")
	rec4 := c.postForm(t, "/account/pgp", f2)
	t.Logf("POST garbage (fresh token) code=%d body=%q", rec4.Code, truncate(rec4.Body.String()))

	// Fresh GET + solve, then submit valid armored key once.
	recGet3, body3 := c.get(t, "/account")
	t.Logf("GET /account #3 code=%d len=%d", recGet3.Code, len(body3))
	f3 := url.Values{}
	f3.Set("csrf_token", extractInput(t, body3, "csrf_token"))
	f3.Set("captcha_id", extractInput(t, body3, "captcha_id"))
	f3.Set("captcha_token", extractInput(t, body3, "captcha_token"))
	f3.Set("captcha_answer", parsePromptSum(t, extractText(t, body3, "CAPTCHA:")))
	f3.Set("pgp_public_key", pubArmor)
	rec5 := c.postForm(t, "/account/pgp", f3)
	t.Logf("POST valid (fresh token) code=%d body=%q", rec5.Code, truncate(rec5.Body.String()))

	// Probe B: same fresh-token flow against /account/phrase — if phrase also
	// 500s, the bug is in the shared account POST plumbing, not PGP-specific.
	bodyB := c.getFreshSolvedPost(t, c, "/account", "/account/phrase", url.Values{"personal_phrase": {"my phrase"}})
	_ = bodyB
}

// helper to keep probes short
func (c *httpClient) getFreshSolvedPost(t *testing.T, _ *httpClient, getTarget, postPath string, extra url.Values) string {
	t.Helper()
	_, body := c.get(t, getTarget)
	f := url.Values{}
	f.Set("csrf_token", extractInput(t, body, "csrf_token"))
	f.Set("captcha_id", extractInput(t, body, "captcha_id"))
	f.Set("captcha_token", extractInput(t, body, "captcha_token"))
	f.Set("captcha_answer", parsePromptSum(t, extractText(t, body, "CAPTCHA:")))
	for k, vs := range extra {
		for _, v := range vs {
			f.Add(k, v)
		}
	}
	rec := c.postForm(t, postPath, f)
	out := fmt.Sprintf("%s %s code=%d body=%q", t.Name(), postPath, rec.Code, truncate(rec.Body.String()))
	t.Log(out)
	return out
}
