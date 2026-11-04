package server

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestZZDiagRenderAccountWithKey(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	password, uid := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{"username": {"alice"}, "password": {password}})
	if rec.Code != 303 {
		t.Fatalf("login=%d", rec.Code)
	}
	pubArmor, _ := generateArmoredKeyPair(t)
	if err := store.AuthSetPGPKey(nilCtx(), uid, pubArmor); err != nil {
		t.Fatal(err)
	}

	// Now GET /account should render with stored key in textarea
	recGet, body := c.get(t, "/account")
	t.Logf("GET /account code=%d len=%d", recGet.Code, len(body))
	if recGet.Code == 500 {
		// reproduce template exec error directly
		data := AuthData{Captcha: nil, HoneypotHidden: true, PGPPublicKey: pubArmor, PGPFingerprint: pgpFingerprint(pubArmor)}
		_ = data
		t.Log("body:", body)
	}
	if !strings.Contains(body, "PUBLIC KEY BLOCK") {
		t.Errorf("key not shown; body snippet: %.500s", body)
	}
}

func nilCtx() context.Context { return context.Background() }
