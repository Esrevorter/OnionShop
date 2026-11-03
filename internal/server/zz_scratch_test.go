package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
)

func TestScratchTokenLifecycle(t *testing.T) {
	sm := NewSessions(scs.New().Store, DefaultSessionConfig())
	var ctxTok string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxTok = sm.Token(r.Context())
	})
	full := sm.LoadAndSave(h)

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/x", nil).WithContext(context.Background())
	full.ServeHTTP(rec1, req1)
	t.Logf("GET#1 no-cookie: handler ctx token=%q", ctxTok)
	var cookie string
	for _, c := range rec1.Result().Cookies() {
		t.Logf("GET#1 Set-Cookie %s=%s", c.Name, c.Value)
		cookie = c.Value
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/x", nil)
	if cookie != "" {
		req2.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	}
	ctxTok = ""
	full.ServeHTTP(rec2, req2)
	t.Logf("POST#2 cookie=%q -> ctx token=%q", cookie, ctxTok)
	for _, c := range rec2.Result().Cookies() {
		t.Logf("POST#2 Set-Cookie %s=%s", c.Name, c.Value)
	}
}
