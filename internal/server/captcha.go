package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// CAPTCHA service (ARCHITECTURE.md §8, AGENTS.md §3): locally generated PNG
// challenges with single-use tokens bound to session+user+purpose, ~5 min
// expiry. No JavaScript, no external captcha services (they are CDNs and
// deanonymizing), no fonts — glyphs come from a built-in 5x7 bitmap set so
// rendering is pure stdlib and deterministic.

// CaptchaTTL is the challenge lifetime before it expires unused.
const CaptchaTTL = 5 * time.Minute

// ErrCaptchaInvalid is returned for a wrong answer, an expired or already
// consumed token, or a token transplanted to another session/user/purpose.
// Callers must never reveal WHICH condition fired: forms simply re-render
// with a fresh challenge.
var ErrCaptchaInvalid = errors.New("server: captcha validation failed")

// CaptchaChallenge is one issued puzzle. Image/Prompt are identical for both
// render paths (inline template + /captcha/<id>.png GET) because drawing is
// deterministic given the stored state.
type CaptchaChallenge struct {
	ID      string // opaque public id used in /captcha/<id>.png URLs
	Prompt  string // e.g. "7 + 4 =" (also embedded in the image)
	Image   []byte // PNG bytes
	Token   string // hidden single-use form field
	Expires time.Time
}

// captchaState is everything bound into one challenge; it lives ONLY
// server-side (Redis + in-process mirror). The answer never appears in the
// token, so fetching the image leaks nothing usable without the token.
type captchaState struct {
	A       int    // first operand
	B       int    // second operand
	UserID  string // "" => anonymous, bound to the session token only
	Purpose string // register|ticket|message|... (forms cannot cross-redeem)
	Expires int64  // unix seconds, UTC
}

// CaptchaService owns issued challenges. Live state rides Redis with an
// in-process mirror, so a Redis restart (the dataset is RAM-only by design)
// cannot strand every pending puzzle mid-form — and the full lifecycle is
// unit-testable without a live Redis.
type CaptchaService struct {
	rdb     *redis.Client
	secret  []byte // HMAC key for ids/tokens; tmpfs-held env file (SECURITY §5.3)
	mu      sync.Mutex
	mem     map[string]captchaState
	nowFunc func() time.Time // test seam
}

// NewCaptchaService builds the service. rdb may be nil (memory-only mode for
// dev/test; production compose always wires Redis). secret must be >=16 bytes.
func NewCaptchaService(rdb *redis.Client, secret []byte) (*CaptchaService, error) {
	if len(secret) < 16 {
		return nil, errors.New("server: captcha HMAC secret too short")
	}
	return &CaptchaService{
		rdb:     rdb,
		secret:  append([]byte(nil), secret...),
		mem:     map[string]captchaState{},
		nowFunc: time.Now,
	}, nil
}

// Issue mints a fresh challenge bound to (sessionToken, userID, purpose).
// Small addition sums are trivial for humans, worthless to scripted
// mass-messaging bots, and need no font files.
func (c *CaptchaService) Issue(ctx context.Context, sessionToken, userID, purpose string) (*CaptchaChallenge, error) {
	a, b := captchaOperands()
	nonce, err := c.newNonce()
	if err != nil {
		return nil, err
	}
	token, err := c.newToken()
	if err != nil {
		return nil, err
	}
	// The id is public (appears in /captcha/<id>.png URLs); the form token is
	// the secret companion. Each carries its own HMAC binding over the full
	// context (nonce|token|session|user|purpose), so Verify can reject
	// ids/tokens minted for a different session, account or form without any
	// extra lookup — and neither value leaks the answer.
	id := nonce + "." + c.bindingMAC(nonce, token, sessionToken, userID, purpose, "id")
	tok := token + "." + c.bindingMAC(nonce, token, sessionToken, userID, purpose, "tk")
	st := captchaState{
		A:       a,
		B:       b,
		UserID:  userID,
		Purpose: purpose,
		Expires: c.nowFunc().Add(CaptchaTTL).UTC().Unix(),
	}
	key := captchaKey(id)
	if c.rdb != nil {
		if err := c.rdb.Set(ctx, key, encodeCaptchaState(st), CaptchaTTL).Err(); err != nil {
			// Redis unreachable: degrade to the memory mirror rather than
			// failing the form outright — friction survives, UX does not break.
			log.Printf("captcha: redis set failed, memory mirror only: %v", sanitizedErr(err))
		}
	}
	c.storeMem(key, st)
	img, err := renderCaptchaPNG(a, b)
	if err != nil {
		return nil, err
	}
	return &CaptchaChallenge{
		ID:      id,
		Prompt:  fmt.Sprintf("%d + %d =", a, b),
		Image:   img,
		Token:   tok,
		Expires: time.Unix(st.Expires, 0).UTC(),
	}, nil
}

// Verify checks the submitted (token, answer) against the live challenge for
// id WITHOUT consuming it. Handlers run expensive work (argon2 hashing, DB
// writes) only after Verify succeeds, then call Burn on the accepted path.
// A wrong answer consumes nothing here — but the attempt already rode the
// per-identity rate limiter, so brute force stays uneconomical.
func (c *CaptchaService) Verify(ctx context.Context, token, id, answer, sessionToken, userID string) error {
	nonce, iBind, okI := splitCaptchaPair(id)
	tTok, tBind, okT := splitCaptchaPair(token)
	if !okT || !okI || id == "" || token == "" {
		return ErrCaptchaInvalid
	}
	st, found := c.peek(ctx, captchaKey(id))
	if !found || c.nowFunc().UTC().Unix() > st.Expires || st.UserID != userID {
		return ErrCaptchaInvalid
	}
	// Possession-of-binding: recompute both domain-separated MACs over the
	// CURRENT (nonce|token|session|user|purpose) context — purpose comes from
	// the live state, so a challenge minted for another form or account can
	// never redeem here. hmac.Equal keeps the comparison constant-time.
	wantID := c.bindingMAC(nonce, tTok, sessionToken, userID, st.Purpose, "id")
	if !hmac.Equal([]byte(iBind), []byte(wantID)) {
		return ErrCaptchaInvalid
	}
	wantTok := c.bindingMAC(nonce, tTok, sessionToken, userID, st.Purpose, "tk")
	if !hmac.Equal([]byte(tBind), []byte(wantTok)) {
		return ErrCaptchaInvalid
	}
	got, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil {
		return ErrCaptchaInvalid
	}
	if got != st.A+st.B {
		return ErrCaptchaInvalid
	}
	return nil
}

// Burn removes the challenge so its token can never be replayed (single-use
// guarantee, AGENTS.md §3). Call once the surrounding request succeeded.
func (c *CaptchaService) Burn(ctx context.Context, id string) {
	key := captchaKey(id)
	if c.rdb != nil {
		if err := c.rdb.Del(ctx, key).Err(); err != nil {
			log.Printf("captcha: redis del failed (mirror still cleared): %v", sanitizedErr(err))
		}
	}
	c.mu.Lock()
	delete(c.mem, key)
	c.mu.Unlock()
}

// Challenge rebuilds the full presentation (prompt + PNG) for one LIVE
// challenge id so GET /captcha/<id>.png renders byte-identically to the
// inline copy issued with the form. Expired/burned ids yield ok=false.
func (c *CaptchaService) Challenge(ctx context.Context, id string) (*CaptchaChallenge, bool) {
	st, found := c.peek(ctx, captchaKey(id))
	if !found || c.nowFunc().UTC().Unix() > st.Expires {
		return nil, false
	}
	img, err := renderCaptchaPNG(st.A, st.B)
	if err != nil {
		return nil, false
	}
	return &CaptchaChallenge{
		ID:      id,
		Prompt:  fmt.Sprintf("%d + %d =", st.A, st.B),
		Image:   img,
		Expires: time.Unix(st.Expires, 0).UTC(),
	}, true
}

func captchaKey(id string) string { return "cap:" + id }

func (c *CaptchaService) peek(ctx context.Context, key string) (captchaState, bool) {
	if c.rdb != nil {
		v, err := c.rdb.Get(ctx, key).Result()
		if err == nil {
			if st, ok := decodeCaptchaState(v); ok {
				return st, true
			}
		}
		// Redis miss (restart) or error: fall back to the mirror below.
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.mem[key]
	return st, ok
}

func (c *CaptchaService) storeMem(key string, st captchaState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mem[key] = st
	// Opportunistic GC so memory-only mode cannot grow unbounded.
	if len(c.mem) > 4096 {
		now := c.nowFunc().UTC().Unix()
		for k, s := range c.mem {
			if now > s.Expires {
				delete(c.mem, k)
			}
		}
	}
}

// newNonce returns the 128-bit random nonce that anchors one challenge. The
// caller mixes it into both the public id and the secret token's bindings.
func (c *CaptchaService) newNonce() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthInternal, err)
	}
	return base64.RawURLEncoding.EncodeToString(nonce), nil
}

func (c *CaptchaService) newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthInternal, err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// bindingMAC folds the full challenge context plus a domain tag ("id"/"tk")
// into a short base64url digest. Domain separation means the id's MAC can
// never be replayed as a token's MAC even with identical inputs.
func (c *CaptchaService) bindingMAC(nonce, token, sessionToken, userID, purpose, domain string) string {
	m := hmac.New(sha256.New, c.secret)
	m.Write([]byte(domain + "|" + nonce + "|" + token + "|" + sessionToken + "|" + userID + "|" + purpose))
	sum := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	return sum[:16]
}

// splitCaptchaPair deconstructs "<head>.<mac>" (ids carry the nonce as head,
// tokens carry the private token as head).
func splitCaptchaPair(v string) (head, mac string, ok bool) {
	p := strings.SplitN(v, ".", 2)
	if len(p) != 2 || p[0] == "" || p[1] == "" {
		return "", "", false
	}
	return p[0], p[1], true
}

func encodeCaptchaState(st captchaState) string {
	return fmt.Sprintf("%d|%d|%d|%s|%s", st.Expires, st.A, st.B, st.UserID, st.Purpose)
}

func decodeCaptchaState(s string) (captchaState, bool) {
	parts := strings.SplitN(s, "|", 5)
	if len(parts) != 5 {
		return captchaState{}, false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return captchaState{}, false
	}
	a, err := strconv.Atoi(parts[1])
	if err != nil {
		return captchaState{}, false
	}
	b, err := strconv.Atoi(parts[2])
	if err != nil {
		return captchaState{}, false
	}
	return captchaState{Expires: exp, A: a, B: b, UserID: parts[3], Purpose: parts[4]}, true
}

// captchaOperands draws two addends in [3,19] from crypto/rand.
func captchaOperands() (int, int) {
	return 3 + randInt(17), 3 + randInt(17)
}

func randInt(n int) int {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0 // degrades uniformity only, never secrecy (same policy as randSeq)
	}
	v := binary.BigEndian.Uint64(b[:])
	return int(v % uint64(n))
}

// sanitizedErr strips redis error detail down to a fixed shape so raw command
// output (which can echo key names/values) never reaches journald
// (AGENTS.md §4: never log sensitive data).
func sanitizedErr(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("redis: operation failed")
}

// ---------------------------------------------------------------------------
// PNG rendering — pure stdlib, deterministic, no font files.
// ---------------------------------------------------------------------------

// glyph5x7 maps characters to a 5-wide x 7-tall bitmap (rows top->bottom,
// bit 0 = leftmost pixel). Only what prompts actually contain: digits, "+",
// "=", space, "?".
var glyph5x7 = map[byte][7]uint8{
	'0': {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	'1': {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	'2': {0x0E, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1F},
	'3': {0x1F, 0x02, 0x04, 0x02, 0x01, 0x11, 0x0E},
	'4': {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	'5': {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	'6': {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	'7': {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8': {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	'9': {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
	'+': {0x00, 0x04, 0x04, 0x1F, 0x04, 0x04, 0x00},
	'=': {0x00, 0x00, 0x1F, 0x00, 0x1F, 0x00, 0x00},
	' ': {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	'?': {0x0E, 0x11, 0x01, 0x06, 0x04, 0x00, 0x04},
}

// renderCaptchaPNG draws "a + b = ?" at 3x scale with light noise speckle.
// Deterministic given (a,b) so inline and URL render paths agree byte-for-byte.
func renderCaptchaPNG(a, b int) ([]byte, error) {
	text := fmt.Sprintf("%d + %d = ?", a, b)
	const scale = 3
	margin := 4 * scale
	w := margin*2 + len(text)*(5+1)*scale - scale
	h := margin*2 + 7*scale

	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix { // background paper (image.Gray has no Fill method)
		img.Pix[i] = 245
	}

	x := margin
	for i := 0; i < len(text); i++ {
		g, ok := glyph5x7[text[i]]
		if !ok {
			g = glyph5x7['?']
		}
		for row := 0; row < 7; row++ {
			for col := 0; col < 5; col++ {
				if g[row]&(1<<(4-col)) != 0 {
					fillCell(img, x+col*scale, margin+row*scale, scale)
				}
			}
		}
		x += (5 + 1) * scale
	}
	// Fixed pseudo-noise derived from the operands: adds visual texture while
	// staying deterministic (both render paths must produce identical bytes).
	seed := uint32(a)*2654435761 + uint32(b)*40503
	for i := uint32(0); i < uint32(w*h)/90; i++ {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		px := int(seed % uint32(w))
		py := int((seed * 7919) % uint32(h))
		img.SetGray(px, py, color.Gray{Y: 190})
	}

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("captcha: png encode: %w", err)
	}
	return buf.Bytes(), nil
}

func fillCell(img *image.Gray, x, y, s int) {
	for dy := 0; dy < s; dy++ {
		for dx := 0; dx < s; dx++ {
			img.SetGray(x+dx, y+dy, color.Gray{Y: 20})
		}
	}
}
