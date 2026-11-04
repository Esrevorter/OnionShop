package server

import (
"bytes"
"context"
"net/http/httptest"
"strings"
"testing"
)

func TestZZRenderCheck(t *testing.T) {
store := newFakeAuthStore(t)
app := newAuthAppForTest(t, store)
r := httptest.NewRequest("GET", "/account", nil)
data := app.baseData(r)
pubArmor, _ := generateArmoredKeyPair(t)
canonical, err := ValidatePGPPublicKeyArmor(pubArmor)
if err != nil {
t.Fatalf("validate: %v", err)
}
data.PGPPublicKey = canonical
data.PGPFingerprint = pgpFingerprint(canonical)
if ch := app.newChallenge(httptest.NewRecorder(), r, accountPurpose); ch != nil {
data.Captcha = ch
}
var buf bytes.Buffer
if err := app.templates["account"].ExecuteTemplate(&buf, "layout", data); err != nil {
t.Fatalf("template exec error: %v", err)
}
_ = context.Background()
_ = strings.TrimSpace
}
