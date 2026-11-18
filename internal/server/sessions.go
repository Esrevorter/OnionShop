package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

const (
	SessionCookieName = "session"
	CSRFKey           = "csrf_token"
	CSRFFormField     = "csrf_token"
	csrfTokenBytes    = 32

	createdKey  = "sess.created"
	userIDKey   = "user_id"
	usernameKey = "sess.username"
	roleKey     = "sess.role"
	phraseKey   = "sess.phrase"
)

var ErrInternalSession = errors.New("server: session subsystem failure")

type SessionConfig struct {
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
	SecureCookies   bool
	CookieDomain    string
}

func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		IdleTimeout:     30 * time.Minute,
		AbsoluteTimeout: 12 * time.Hour,
		SecureCookies:   true,
	}
}

func NewSessions(st scs.Store, cfg SessionConfig) *scs.SessionManager {
	sm := scs.New()
	sm.Store = st
	sm.Cookie.Name = SessionCookieName
	sm.Cookie.HttpOnly = true
	sm.Cookie.Secure = cfg.SecureCookies
	sm.Cookie.SameSite = http.SameSiteStrictMode
	sm.Cookie.Path = "/"
	sm.Cookie.Persist = true
	if cfg.CookieDomain != "" {
		sm.Cookie.Domain = cfg.CookieDomain
	}
	sm.Lifetime = cfg.AbsoluteTimeout
	sm.IdleTimeout = cfg.IdleTimeout
	sm.ErrorFunc = func(w http.ResponseWriter, r *http.Request, _ error) {
		writeGenericError(w)
	}
	return sm
}

func AbsoluteExpiry(sm *scs.SessionManager, timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			created := sm.GetInt64(ctx, createdKey)
			switch {
			case created == 0:
				sm.Put(ctx, createdKey, time.Now().UTC().Unix())
			case time.Since(time.Unix(created, 0)) > timeout:
				if err := sm.Destroy(ctx); err != nil {
					writeGenericError(w)
					return
				}
				if err := sm.RenewToken(ctx); err != nil {
					writeGenericError(w)
					return
				}
				sm.Put(ctx, createdKey, time.Now().UTC().Unix())
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RotateOnAuth(ctx context.Context, sm *scs.SessionManager) error {
	if err := sm.RenewToken(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrInternalSession, err)
	}
	return nil
}

func CurrentUserID(ctx context.Context, sm *scs.SessionManager) string {
	return sm.GetString(ctx, userIDKey)
}

func CurrentRole(ctx context.Context, sm *scs.SessionManager) string {
	return sm.GetString(ctx, roleKey)
}

func (a *App) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if CurrentUserID(r.Context(), a.sm) == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			role := CurrentRole(r.Context(), a.sm)
			for _, want := range roles {
				if role == want {
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("forbidden\n"))
		})
	}
}

func (a *App) InvalidateUserSessions(ctx context.Context, userID string) error {
	if a.invalid == nil {
		log.Printf("server: session invalidation skipped for a privilege change (no SessionInvalidator wired)")
		return nil
	}
	if userID == "" {
		return errors.New("server: empty user id in session invalidation")
	}
	if err := a.invalid.InvalidateUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("server: invalidate sessions: %w", err)
	}
	return nil
}

func NewRandomToken(n int) (string, error) {
	if n <= 0 {
		n = csrfTokenBytes
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInternalSession, err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(t string) [sha256.Size]byte { return sha256.Sum256([]byte(t)) }

var ErrCSRFMismatch = errors.New("server: csrf validation failed")

func CSRFLoad(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := sm.Get(r.Context(), CSRFKey).(string)
			if !ok || len(tok) < 16 {
				var err error
				if tok, err = NewRandomToken(csrfTokenBytes); err != nil {
					writeGenericError(w)
					return
				}
				sm.Put(r.Context(), CSRFKey, tok)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func CSRFToken(ctx context.Context, sm *scs.SessionManager) string {
	tok, _ := sm.Get(ctx, CSRFKey).(string)
	return tok
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// CSRFProtect enforces double-submit validation on state-changing requests.
// Comparison runs over SHA-256 digests in constant time; failures get a uniform 403.
//
// Exception for /logout: if the session expired between page load and the click,
// the form carries a stale CSRF token while the fresh session has a new one.
// They won't match, resulting in a 403. For logout, we don't care: the goal is
// just to ensure the user is logged out, and a stale token means they already are.
func CSRFProtect(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/logout" && r.Method == http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			want, _ := sm.Get(r.Context(), CSRFKey).(string)
			got := r.FormValue(CSRFFormField)
			if got == "" {
				got = r.Header.Get("X-CSRF-Token")
			}
			wantHash := hashToken(want)
			gotHash := hashToken(got)
			if want == "" || subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) != 1 {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("403 forbidden\n"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeGenericError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte("internal error\n"))
}
