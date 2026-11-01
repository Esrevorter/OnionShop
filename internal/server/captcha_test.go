package server

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newTestCaptcha returns a memory-only CaptchaService (rdb nil) with a fixed
// clock so expiry is testable without sleeping past 5 real minutes.
func newTestCaptcha(t *testing.T) (*CaptchaService, *time.Time) {
	t.Helper()
	c, err := NewCaptchaService(nil, []byte("test-secret-test-secret"))
	if err != nil {
		t.Fatalf("NewCaptchaService: %v", err)
	}
	now := time.Now().UTC()
	c.nowFunc = func() time.Time { return now }
	return c, &now
}

func TestCaptchaIssueShape(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ch, err := c.Issue(context.Background(), "sess-1", "", "register")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if ch.ID == "" || ch.Token == "" || len(ch.Image) == 0 {
		t.Fatalf("empty challenge fields: %+v", ch)
	}
	if !strings.HasSuffix(ch.Prompt, "=") || !strings.Contains(ch.Prompt, "+") {
		t.Errorf("prompt not an addition: %q", ch.Prompt)
	}
	// Image must decode as a real PNG.
	img, err := png.Decode(bytes.NewReader(ch.Image))
	if err != nil {
		t.Fatalf("challenge image is not a valid PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() < 40 || b.Dy() < 20 {
		t.Errorf("captcha image implausibly small: %dx%d", b.Dx(), b.Dy())
	}
	// Two issues never collide.
	ch2, _ := c.Issue(context.Background(), "sess-1", "", "register")
	if ch2.ID == ch.ID || ch2.Token == ch.Token {
		t.Error("two challenges share id/token material")
	}
}

func TestCaptchaVerifyHappyPath(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ctx := context.Background()
	ch, err := c.Issue(ctx, "sess-1", "", "message")
	if err != nil {
		t.Fatal(err)
	}
	answer := promptSum(t, ch.Prompt)
	if err := c.Verify(ctx, ch.Token, ch.ID, answer, "sess-1", ""); err != nil {
		t.Fatalf("valid answer rejected: %v", err)
	}
	// Verify does NOT consume: same pair still verifies until Burn.
	if err := c.Verify(ctx, ch.Token, ch.ID, answer, "sess-1", ""); err != nil {
		t.Fatalf("second verify before burn failed: %v", err)
	}
	c.Burn(ctx, ch.ID)
	if err := c.Verify(ctx, ch.Token, ch.ID, answer, "sess-1", ""); !errors.Is(err, ErrCaptchaInvalid) {
		t.Fatalf("replay after Burn: got %v, want ErrCaptchaInvalid", err)
	}
}

func TestCaptchaWrongAnswer(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ctx := context.Background()
	ch, _ := c.Issue(ctx, "s", "", "message")
	sum, err := strconv.Atoi(promptSum(t, ch.Prompt))
	if err != nil {
		t.Fatalf("promptSum returned non-numeric answer: %v", err)
	}
	wrong := sum + 1
	if err := c.Verify(ctx, ch.Token, ch.ID, strconv.Itoa(wrong), "s", ""); !errors.Is(err, ErrCaptchaInvalid) {
		t.Fatalf("wrong answer accepted: %v", err)
	}
	for _, junk := range []string{"", " ", "abc", "-99999"} {
		if err := c.Verify(ctx, ch.Token, ch.ID, junk, "s", ""); !errors.Is(err, ErrCaptchaInvalid) {
			t.Errorf("junk answer %q accepted", junk)
		}
	}
}

func TestCaptchaExpiry(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ctx := context.Background()
	ch, _ := c.Issue(ctx, "s", "u1", "ticket")
	answer := promptSum(t, ch.Prompt)

	// Jump the fixed clock past TTL.
	c.nowFunc = func() time.Time { return ch.Expires.Add(time.Second) }
	if err := c.Verify(ctx, ch.Token, ch.ID, answer, "s", "u1"); !errors.Is(err, ErrCaptchaInvalid) {
		t.Fatalf("expired challenge accepted: %v", err)
	}
	if _, ok := c.Challenge(ctx, ch.ID); ok {
		t.Error("expired challenge still renders")
	}
}

func TestCaptchaSingleUseAfterBurn(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ctx := context.Background()
	ch, _ := c.Issue(ctx, "s", "", "register")
	answer := promptSum(t, ch.Prompt)
	c.Burn(ctx, ch.ID)
	// Both the direct verify and the render path must treat it as gone.
	if err := c.Verify(ctx, ch.Token, ch.ID, answer, "s", ""); !errors.Is(err, ErrCaptchaInvalid) {
		t.Fatalf("burned token verified: %v", err)
	}
	if _, ok := c.Challenge(ctx, ch.ID); ok {
		t.Error("burned challenge still renders via Challenge()")
	}
	// Burning twice is harmless.
	c.Burn(ctx, ch.ID)
}

func TestCaptchaCrossSessionRejected(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ctx := context.Background()
	ch, _ := c.Issue(ctx, "sess-A", "", "message")
	answer := promptSum(t, ch.Prompt)
	if err := c.Verify(ctx, ch.Token, ch.ID, answer, "sess-B", ""); !errors.Is(err, ErrCaptchaInvalid) {
		t.Fatal("token minted for sess-A redeemed under sess-B")
	}
	// A different user cannot redeem another user's challenge either.
	chU, _ := c.Issue(ctx, "sess-A", "alice", "ticket")
	ansU := promptSum(t, chU.Prompt)
	if err := c.Verify(ctx, chU.Token, chU.ID, ansU, "sess-A", "mallory"); !errors.Is(err, ErrCaptchaInvalid) {
		t.Fatal("cross-user redemption accepted")
	}
	// Correct holder still works.
	if err := c.Verify(ctx, chU.Token, chU.ID, ansU, "sess-A", "alice"); err != nil {
		t.Fatalf("legitimate holder rejected: %v", err)
	}
}

func TestCaptchaForeignTokenRejected(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ctx := context.Background()
	ch, _ := c.Issue(ctx, "s", "", "message")
	forged := []struct{ token, id string }{
		{"", ""},
		{"no-dot-here", ch.ID},
		{"aaaa.bbbb", "cccc.dddd"}, // well-formed but unknown binding
		{ch.Token, strings.Replace(ch.ID, ".", "_", 1)}, // mangled id
	}
	for _, f := range forged {
		if err := c.Verify(ctx, f.token, f.id, "10", "s", ""); !errors.Is(err, ErrCaptchaInvalid) {
			t.Errorf("forged pair (%q,%q) accepted", f.token, f.id)
		}
	}
}

func TestCaptchaRenderDeterministic(t *testing.T) {
	a, err := renderCaptchaPNG(7, 4)
	if err != nil {
		t.Fatal(err)
	}
	b, err := renderCaptchaPNG(7, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("same operands rendered differently — inline vs URL paths would disagree")
	}
	c, _ := renderCaptchaPNG(8, 4)
	if bytes.Equal(a, c) {
		t.Error("different operands rendered identically")
	}
}

func TestCaptchaChallengeRenderMatchesIssue(t *testing.T) {
	c, _ := newTestCaptcha(t)
	ctx := context.Background()
	ch, _ := c.Issue(ctx, "s", "u", "message")
	got, ok := c.Challenge(ctx, ch.ID)
	if !ok {
		t.Fatal("live challenge not found by id")
	}
	if !bytes.Equal(got.Image, ch.Image) {
		t.Error("/captcha/<id>.png render differs from the issued inline image")
	}
	if got.Prompt != ch.Prompt {
		t.Errorf("prompt mismatch: %q vs %q", got.Prompt, ch.Prompt)
	}
}

func TestNewCaptchaServiceRejectsShortSecret(t *testing.T) {
	if _, err := NewCaptchaService(nil, []byte("short")); err == nil {
		t.Fatal("accepted a tiny HMAC secret")
	}
}

// promptSum evaluates the minted "a + b =" prompt — exactly what a human
// reads off the image. Tests use it to avoid hardcoding random operands.
func promptSum(t *testing.T, prompt string) string {
	t.Helper()
	fields := strings.Fields(prompt) // ["7", "+", "4", "="]
	if len(fields) != 4 || fields[1] != "+" {
		t.Fatalf("unexpected prompt shape %q", prompt)
	}
	a, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("bad operand %q: %v", fields[0], err)
	}
	b, err := strconv.Atoi(fields[2])
	if err != nil {
		t.Fatalf("bad operand %q: %v", fields[2], err)
	}
	return strconv.Itoa(a + b)
}
