package server

import (
"net/http"
"net/url"
"os"
"log"
"testing"
)

func TestZZPin500(t *testing.T) {
log.SetOutput(os.Stdout)
defer log.SetOutput(os.Stderr)
store := newFakeAuthStore(t)
password, uid := store.seedUser(t, "alice", "customer")
t.Logf("uid canonical=%q", uuidString(uid))
app := newAuthAppForTest(t, store)
c := newHTTPClient(app)
rec, _ := postForm(t, app, "/login", c, "/login", url.Values{"username": {"alice"}, "password": {password}})
if rec.Code != http.StatusSeeOther { t.Fatalf("login=%d", rec.Code) }
pubArmor, _ := generateArmoredKeyPair(t)
rec2, body2 := postForm(t, app, "/account", c, "/account/pgp", url.Values{"pgp_public_key": {pubArmor}})
t.Logf("upload code=%d body=%.120s", rec2.Code, body2)
}
