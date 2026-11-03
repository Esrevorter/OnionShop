package server

import (
"context"
"net/http"
"net/http/httptest"
"path/filepath"
"strings"
"testing"

"github.com/alexedwards/scs/v2"
)

func TestScratchTokenLifecycle(t *testing.T) {
sm := NewSessions(scs.New().Store, DefaultSessionConfig())
h := func(w http.ResponseWriter, r *http.Request) {}

// GET with NO cookie — does LoadAndSave commit a token into the SAME ctx?
rec1 := httptest.NewRecorder()
req1 := httptest.NewRequest("GET", "/x", nil).WithContext(context.Background())
ctxTok1 := ""
sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
sm.Put(r.Context(), "seed", "1") // force non-empty session
ctxTok1 = sm.Token(r.Context())
})).ServeHTTP(rec1, req1)
t.Logf("no-cookie GET: ctx token during handler=%q", ctxTok1)
for _, c := range rec1.Result().Cookies() {
t.Logf("no-cookie GET: Set-Cookie %s=%s", c.Name, c.Value)
}

dir, _ := filepath.Abs(filepath.Join("..", "..", "web", "templates"))
_ = dir
_ = strings.TrimSpace
}
