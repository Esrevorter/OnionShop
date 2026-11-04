package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// Phase 3 PGP tests: pure validation units (public round-trip, private-key
// rejection, size/shape guards) plus full-chain upload/clear handler tests
// through the real router, fake store, and solved CAPTCHA friction.

// ---- local key fixtures -----------------------------------------------------

// armorEntity serializes an entity's raw packet stream (public or private)
// under the matching ASCII armor header. go-crypto's Entity.Serialize /
// SerializePrivate emit BINARY packets — production code must armor them
// itself, and these fixtures mirror that contract so tests exercise the same
// shapes the handlers receive.
func armorEntity(t *testing.T, body []byte, blockType string) string {
	t.Helper()
	var out bytes.Buffer
	w, err := armor.Encode(&out, blockType, nil)
	if err != nil {
		t.Fatalf("armor.Encode(%q): %v", blockType, err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("armor write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("armor close: %v", err)
	}
	return out.String()
}

func generateArmoredKeyPair(t *testing.T) (pubArmor, privArmor string) {
	t.Helper()
	ent, err := openpgp.NewEntity("Test User", "", "test@example.com", nil)
	if err != nil {
		t.Fatalf("openpgp.NewEntity: %v", err)
	}
	var pubBin, privBin bytes.Buffer
	if err := ent.Serialize(&pubBin); err != nil {
		t.Fatalf("serialize public: %v", err)
	}
	if err := ent.SerializePrivate(&privBin, nil); err != nil {
		t.Fatalf("serialize private: %v", err)
	}
	return armorEntity(t, pubBin.Bytes(), openpgp.PublicKeyType),
		armorEntity(t, privBin.Bytes(), openpgp.PrivateKeyType)
}

// armorWithBlockType re-arms binary key material under an arbitrary armor
// header — used to forge a "PGP PRIVATE KEY BLOCK" wrapper around otherwise
// valid public packets (type-level rejection test).
func armorWithBlockType(t *testing.T, body []byte, blockType string) string {
	t.Helper()
	var out bytes.Buffer
	w, err := armor.Encode(&out, blockType, nil)
	if err != nil {
		t.Fatalf("armor.Encode(%q): %v", blockType, err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("armor write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("armor close: %v", err)
	}
	return out.String()
}

// ---- validation units -------------------------------------------------------

func TestValidatePGPPublicKeyRoundTrip(t *testing.T) {
	pubArmor, _ := generateArmoredKeyPair(t)
	got, err := ValidatePGPPublicKeyArmor(pubArmor)
	if err != nil {
		t.Fatalf("valid public key rejected: %v", err)
	}
	if !strings.HasPrefix(got, "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		t.Errorf("canonical output is not an armored public block:\n%.80s", got)
	}
	// The User ID packet lives inside base64-armored binary, so it can never
	// appear as plaintext in the block. Verify structurally instead: re-parse
	// the canonical armor and confirm the identity survived normalization.
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(got))
	if err != nil {
		t.Fatalf("re-parsing canonical armor: %v", err)
	}
	if len(ring) != 1 {
		t.Fatalf("canonical key ring has %d entities, want 1", len(ring))
	}
	foundUID := false
	for _, ident := range ring[0].Identities {
		if strings.Contains(ident.Name, "test@example.com") {
			foundUID = true
		}
	}
	if !foundUID {
		t.Error("canonical output dropped the user ID packet")
	}
	// Canonical text must itself re-validate (idempotent normalization).
	again, err := ValidatePGPPublicKeyArmor(got)
	if err != nil {
		t.Fatalf("canonical key failed re-validation: %v", err)
	}
	if again != got {
		t.Error("validation is not idempotent on canonical input")
	}
	// Fingerprint display helper works on the stored form.
	fp := pgpFingerprint(got)
	if len(fp) < 32 || strings.ToUpper(fp) != fp {
		t.Errorf("pgpFingerprint = %q, want long uppercase hex", fp)
	}
	if pgpFingerprint("") != "" {
		t.Error("pgpFingerprint of empty input should be empty")
	}
}

func TestValidatePGPRejectsPrivateKey(t *testing.T) {
	_, privArmor := generateArmoredKeyPair(t)
	if !strings.Contains(privArmor, "PRIVATE KEY BLOCK") {
		t.Fatal("fixture is not armored private material")
	}
	_, err := ValidatePGPPublicKeyArmor(privArmor)
	if !errors.Is(err, ErrPGPPrivateKey) {
		t.Fatalf("private key error = %v, want ErrPGPPrivateKey", err)
	}
}

func TestValidatePGPRejectsMixedAndForgedBlocks(t *testing.T) {
	pubArmor, _ := generateArmoredKeyPair(t)

	// Forged: public packets wrapped in a PRIVATE KEY BLOCK header.
	block, err := armor.Decode(strings.NewReader(pubArmor))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	body, err := io.ReadAll(block.Body)
	if err != nil {
		t.Fatalf("read fixture body: %v", err)
	}
	forged := armorWithBlockType(t, body, openpgp.PrivateKeyType)
	if _, err := ValidatePGPPublicKeyArmor(forged); !errors.Is(err, ErrPGPPrivateKey) {
		t.Fatalf("forged private-header block error = %v, want ErrPGPPrivateKey", err)
	}

	// Wrong armor type entirely (signature block) must be refused uniformly.
	msg := armorWithBlockType(t, []byte{0xc1, 0x00}, "PGP MESSAGE")
	if _, err := ValidatePGPPublicKeyArmor(msg); err == nil {
		t.Fatal("non public-key armor accepted")
	}

	// Public packets followed by raw private packets: pre-scan catches it even
	// though the first entity parses fine.
	_, privArmor := generateArmoredKeyPair(t)
	privBlock, err := armor.Decode(strings.NewReader(privArmor))
	if err != nil {
		t.Fatalf("decode private fixture: %v", err)
	}
	privBody, err := io.ReadAll(privBlock.Body)
	if err != nil {
		t.Fatalf("read private body: %v", err)
	}
	mixed := append(append([]byte(nil), body...), privBody...)
	mixedArmor := armorWithBlockType(t, mixed, openpgp.PublicKeyType)
	if _, err := ValidatePGPPublicKeyArmor(mixedArmor); !errors.Is(err, ErrPGPPrivateKey) {
		t.Fatalf("mixed stream error = %v, want ErrPGPPrivateKey", err)
	}
}

func TestValidatePGPRejectsGarbageEmptyAndOversize(t *testing.T) {
	if _, err := ValidatePGPPublicKeyArmor("   "); err == nil {
		t.Error("empty submission accepted")
	}
	if _, err := ValidatePGPPublicKeyArmor("not a pgp key at all"); err == nil {
		t.Error("garbage accepted")
	}
	huge := "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n" +
		strings.Repeat("A", pgpKeyMaxBytes+16) +
		"\n-----END PGP PUBLIC KEY BLOCK-----\n"
	if _, err := ValidatePGPPublicKeyArmor(huge); err == nil {
		t.Error("oversized submission accepted")
	}
}

// ---- full-chain handler tests -----------------------------------------------

func TestPGPUploadAndClearFullChain(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	password, uid := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	// Sign in first — /account* routes sit behind the account middleware.
	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login POST = %d, want 303 (body %q)", rec.Code, truncate(rec.Body.String()))
	}

	pubArmor, privArmor := generateArmoredKeyPair(t)

	// Upload a valid public key through GET /account → solve CAPTCHA → POST.
	rec, _ = postForm(t, app, "/account", c, "/account/pgp", url.Values{
		"pgp_public_key": {pubArmor},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PGP upload = %d, want 200 (body %q)", rec.Code, truncate(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Public key saved.") {
		t.Error("success flash missing from upload response")
	}
	if !strings.Contains(body, "PGP PUBLIC KEY BLOCK") {
		t.Error("stored key not displayed after upload")
	}
	if fp := pgpFingerprint(pubArmor); fp != "" && !strings.Contains(body, fp) {
		t.Errorf("fingerprint %s not shown after upload", fp)
	}
	stored, err := store.AuthGetPGPKey(context.Background(), uid)
	if err != nil || stored == "" {
		t.Fatalf("store did not persist key: stored=%q err=%v", truncate(stored), err)
	}

	// A private key must be rejected and never persisted.
	beforeKey := stored
	rec, _ = postForm(t, app, "/account", c, "/account/pgp", url.Values{
		"pgp_public_key": {privArmor},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("private-key upload = %d, want 422 (body %q)", rec.Code, truncate(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "not accepted") {
		t.Error("rejection message missing")
	}
	afterKey, err := store.AuthGetPGPKey(context.Background(), uid)
	if err != nil || afterKey != beforeKey {
		t.Error("private-key attempt mutated the stored key")
	}
	if strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Error("response echoed private key material")
	}

	// Clear removes store + session cache.
	rec, _ = postForm(t, app, "/account", c, "/account/pgp/clear", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("PGP clear = %d, want 200 (body %q)", rec.Code, truncate(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "Public key removed.") {
		t.Error("clear flash missing")
	}
	cleared, err := store.AuthGetPGPKey(context.Background(), uid)
	if err != nil || cleared != "" {
		t.Errorf("key survived clear: %q err=%v", truncate(cleared), err)
	}
}

func TestPGPUploadStoreFailureRendersFreshChallenge(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	password, _ := store.seedUser(t, "alice", "customer")
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	rec, _ := postForm(t, app, "/login", c, "/login", url.Values{
		"username": {"alice"},
		"password": {password},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login POST = %d, want 303", rec.Code)
	}

	store.setPGPErr = errors.New("insert into public.users failed")
	pubArmor, _ := generateArmoredKeyPair(t)
	rec, _ = postForm(t, app, "/account", c, "/account/pgp", url.Values{
		"pgp_public_key": {pubArmor},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("store-failure upload = %d, want 422 (body %q)", rec.Code, truncate(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "could not save the key") {
		t.Error("store failure message missing")
	}
	// The fail() path must render a USABLE form: fresh captcha inputs present.
	if !strings.Contains(body, `name="captcha_token"`) || !strings.Contains(body, `name="csrf_token"`) {
		t.Error("failure render lacks friction fields (nil/stale Captcha regression)")
	}
}

func TestPGPToolPageServesExplainer(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	rec, body := c.get(t, "/pgp")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /pgp = %d, want 200 (body %q)", rec.Code, truncate(body))
	}
	for _, want := range []string{"PGP public keys", "never", "PUBLIC"} {
		if !strings.Contains(body, want) {
			t.Errorf("/pgp page missing %q", want)
		}
	}
}

func TestPGPRoutesRequireSession(t *testing.T) {
	warnLogTo(t)
	store := newFakeAuthStore(t)
	app := newAuthAppForTest(t, store)
	c := newHTTPClient(app)

	// Anonymous POSTs to the account-gated PGP endpoints get redirected, not
	// processed (account middleware).
	rec, _ := c.do(t, http.MethodPost, "/account/pgp",
		strings.NewReader(url.Values{"pgp_public_key": {"x"}}.Encode()),
		"application/x-www-form-urlencoded")
	if rec.Code == http.StatusOK || rec.Code == http.StatusUnprocessableEntity {
		t.Fatalf("anonymous PGP upload = %d, want redirect/error, not a processed form", rec.Code)
	}
}
