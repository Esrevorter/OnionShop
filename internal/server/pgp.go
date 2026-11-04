package server

// Phase 3 PGP surface (TASKS.md): public-key upload/validation plus the
// self-service /pgp tool page. Hard OpSec rule (SECURITY_AND_OPSEC.md §1):
// only PUBLIC keys are ever accepted or stored — private keys are rejected at
// validation time and never reach the database, and the server performs no
// decryption whatsoever.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

const (
	// pgpKeyMaxBytes bounds an uploaded armored key (64 KiB). Real user keys
	// are ~4-8 KiB; anything near the cap is either bulk-key material or an
	// abuse attempt, so both are refused before parsing (bounded work per
	// request — AGENTS.md §4).
	pgpKeyMaxBytes = 64 << 10

	// accountPurpose namespaces the CAPTCHA slot embedded on the account
	// page. ONE purpose serves every POST that consumes it (/account/phrase,
	// /account/pgp, /account/pgp/clear): Verify reads the purpose back from
	// the stored challenge state, so mint and verify must agree on this exact
	// string for the binding MAC to match across the GET→POST pair.
	accountPurpose = "account"

	// pgpKeySessionKey caches the validated armored key in the session after
	// a successful upload/clear, mirroring how phraseKey mirrors the DB row:
	// the store is truth, the session is the read cache.
	pgpKeySessionKey = "pgp_public_key"
)

// ErrPGPPrivateKey is returned when the submitted armor contains secret-key
// material. Handlers show a generic-but-honest message; tests match on this
// sentinel instead of error strings.
var ErrPGPPrivateKey = errors.New("server: private key material detected")

// ValidatePGPPublicKeyArmor validates an ASCII-armored OpenPGP submission and
// returns the canonical armored text on success. Rejections:
//   - empty or oversized input,
//   - not an armored public-key block,
//   - any PRIVATE-KEY packet (secret key or secret subkey), detected with a
//     raw pre-scan over the packet stream BEFORE building a key ring, so
//     private material cannot hide inside a composite upload,
//   - unparseable key ring,
//   - zero user IDs (a bare key identifies nobody),
//   - expired primary key.
//
// This function performs pure structural validation: no decryption, no
// network access, nothing secret ever persisted.
func ValidatePGPPublicKeyArmor(submission string) (string, error) {
	trimmed := strings.TrimSpace(submission)
	if trimmed == "" {
		return "", errors.New("server: no key data submitted")
	}
	if len(trimmed) > pgpKeyMaxBytes {
		return "", fmt.Errorf("server: key too large (max %d bytes)", pgpKeyMaxBytes)
	}

	// Pre-scan: decode armor and walk the RAW packet stream looking for
	// secret-key packets. This runs before ReadArmoredKeyRing so a submission
	// that mixes public and private material is rejected outright rather than
	// silently parsed as "the first public entity".
	bin, err := pgpArmorBody(trimmed)
	if err != nil {
		return "", err
	}
	if hasPrivatePacket(bin) {
		return "", ErrPGPPrivateKey
	}

	// Full key-ring parse from the original armored text.
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(trimmed))
	if err != nil {
		return "", fmt.Errorf("server: invalid public key: %w", err)
	}
	if len(ring) == 0 {
		return "", errors.New("server: no keys found in submission")
	}
	primary := ring[0]
	if primary.PrimaryKey == nil {
		return "", errors.New("server: key ring has no primary key")
	}
	if len(primary.Identities) == 0 {
		return "", errors.New("server: key carries no user ID; refusing anonymous key")
	}
	if primary.Revoked(time.Now()) {
		return "", errors.New("server: this key has been revoked")
	}
	// Key expiry lives in the self-certification (KeyLifetimeSecs), not on the
	// key packet itself — v4 keys carry it in the identity self-sig, v6 keys
	// in the direct-key signature.
	if exp := pgpKeyExpiry(primary); !exp.IsZero() && time.Now().After(exp) {
		return "", errors.New("server: this key has expired")
	}
	// Only the primary public key and its self-signed identities may be
	// stored; re-serializing through the library normalizes the armor and
	// drops pasted decoration or trailing packets. Entity.Serialize emits
	// the RAW packet stream, so we must wrap it in ASCII armor ourselves —
	// go-crypto does not (SECURITY_AND_OPSEC.md: only armored public blocks
	// are ever persisted or displayed).
	var rawBin bytes.Buffer
	if err := primary.Serialize(&rawBin); err != nil {
		return "", fmt.Errorf("server: could not normalize key: %w", err)
	}
	var out bytes.Buffer
	aw, err := armor.Encode(&out, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("server: could not normalize key: %w", err)
	}
	if _, err := aw.Write(rawBin.Bytes()); err != nil {
		return "", fmt.Errorf("server: could not normalize key: %w", err)
	}
	if err := aw.Close(); err != nil {
		return "", fmt.Errorf("server: could not normalize key: %w", err)
	}
	return out.String(), nil
}

// pgpArmorBody decodes an ASCII-armored block and returns its binary body.
// Key blocks must be PUBLIC KEY blocks; anything else (signatures, messages,
// split headers) is refused with one uniform error shape — malformed input
// and wrong-type input are indistinguishable to the submitter.
func pgpArmorBody(armored string) ([]byte, error) {
	block, err := armor.Decode(strings.NewReader(armored))
	if err != nil {
		return nil, errors.New("server: expected an ASCII-armored public key block")
	}
	if block.Type != openpgp.PublicKeyType {
		// PRIVATE KEY BLOCK armor is caught here too; hasPrivatePacket gives
		// the precise reason for mixed streams. Keep the rejection honest but
		// never echo key material back.
		if block.Type == openpgp.PrivateKeyType {
			return nil, ErrPGPPrivateKey
		}
		return nil, errors.New("server: expected an ASCII-armored public key block")
	}
	body, err := io.ReadAll(io.LimitReader(block.Body, pgpKeyMaxBytes+1))
	if err != nil {
		return nil, errors.New("server: expected an ASCII-armored public key block")
	}
	if len(body) > pgpKeyMaxBytes {
		return nil, fmt.Errorf("server: key too large (max %d bytes)", pgpKeyMaxBytes)
	}
	return body, nil
}

// hasPrivatePacket walks the raw packet stream and reports whether any
// SecretKey/SecretSubkey packet appears. Unknown/unreadable packets stop the
// walk (the subsequent ring parse produces the authoritative error).
func hasPrivatePacket(bin []byte) bool {
	reader := packet.NewReader(bytes.NewReader(bin))
	for {
		p, err := reader.Next()
		if err != nil {
			return false
		}
		if _, ok := p.(*packet.PrivateKey); ok {
			return true
		}
	}
}

// pgpKeyExpiry derives the primary key's expiration from its self-
// certification: CreationTime + KeyLifetimeSecs. Zero means "never expires".
func pgpKeyExpiry(e *openpgp.Entity) time.Time {
	sigs := make([]*packet.Signature, 0, len(e.Identities)+1)
	if e.SelfSignature != nil {
		sigs = append(sigs, e.SelfSignature)
	}
	for _, id := range e.Identities {
		if id.SelfSignature != nil {
			sigs = append(sigs, id.SelfSignature)
		}
	}
	var latest time.Time
	for _, sig := range sigs {
		if sig.KeyLifetimeSecs == nil || *sig.KeyLifetimeSecs == 0 {
			continue
		}
		exp := sig.CreationTime.Add(time.Duration(*sig.KeyLifetimeSecs) * time.Second)
		if exp.After(latest) {
			latest = exp
		}
	}
	return latest
}

// ---- account-page integration ---------------------------------------------

// pgpFingerprint returns the uppercase hex fingerprint of the stored key for
// display, or "" when absent/undisplayable. Parsing failures degrade to ""
// (the UI simply shows the raw key) — never a request failure.
func pgpFingerprint(armored string) string {
	if armored == "" {
		return ""
	}
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil || len(ring) == 0 || ring[0].PrimaryKey == nil {
		return ""
	}
	return strings.ToUpper(fmt.Sprintf("%X", ring[0].PrimaryKey.Fingerprint))
}

// loadPGPState fills the PGP fields of AuthData: session cache first, then the
// durable store (a cache miss backfills the session so later GETs stay cheap).
func (a *App) loadPGPState(ctx context.Context, data *AuthData) {
	data.PGPPublicKey = a.sm.GetString(ctx, pgpKeySessionKey)
	if data.PGPPublicKey == "" && a.auth != nil {
		if uid := currentUserUUID(ctx, a.sm); uid != nil {
			stored, err := a.auth.AuthGetPGPKey(ctx, *uid)
			if err != nil {
				log.Printf("pgp: key load failed (non-fatal): %v", sanitizedErr(err))
				return
			}
			data.PGPPublicKey = stored
			if stored != "" {
				a.sm.Put(ctx, pgpKeySessionKey, stored)
			}
		}
	}
	data.PGPFingerprint = pgpFingerprint(data.PGPPublicKey)
}

// handlePGPUpload accepts a new armored public key, validates it, persists it,
// and refreshes session state. Every failure path mints a FRESH challenge
// against the same session anchor requireFriction reconstructs
// (sessionTokenForRequest's rotated-session fallback keeps the GET-mint and
// POST-verify anchors consistent), so fail() renders never carry a nil or
// stale Captcha.
func (a *App) handlePGPUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	fail := func(msg string) {
		data.Error = msg
		data.PersonalPhrase = a.sm.GetString(ctx, phraseKey)
		a.loadPGPState(ctx, &data)
		if data.Captcha = a.newChallenge(w, r, accountPurpose); data.Captcha == nil {
			return // newChallenge already answered the generic 500
		}
		a.render(w, "account", http.StatusUnprocessableEntity, data)
	}

	if honeypotTriggered(r) {
		fail("submission rejected")
		return
	}
	if !a.requireFriction(w, r, accountPurpose) {
		fail("challenge failed, please try again")
		return
	}

	canonical, err := ValidatePGPPublicKeyArmor(r.FormValue("pgp_public_key"))
	if err != nil {
		log.Printf("pgp: upload validation rejected key: %v", sanitizedErr(err))
		fail("that key was not accepted; submit a valid ASCII-armored PUBLIC key (private keys are never stored)")
		return
	}
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil {
		writeGenericError(w)
		return
	}
	if err := a.auth.AuthSetPGPKey(ctx, *uid, canonical); err != nil {
		log.Printf("pgp: key save failed: %v", err)
		fail("could not save the key, please try again")
		return
	}
	// Success path: render WITHOUT minting a fresh challenge. requireFriction
	// just consumed (burned + popped the pending stash) this request's anchor;
	// minting here would bind the new puzzle to an identifier that may differ
	// from what scs commits at flush, poisoning the NEXT post. The success
	// page shows the stored key; any subsequent GET/POST mints its own
	// challenge normally.
	a.sm.Put(ctx, pgpKeySessionKey, canonical)
	data.PGPPublicKey = canonical
	data.PGPFingerprint = pgpFingerprint(canonical)
	data.Flash = "Public key saved."
	a.render(w, "account", http.StatusOK, data)
}

// handlePGPClear removes the stored public key (store + session cache).
func (a *App) handlePGPClear(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	data := a.baseData(r)
	fail := func(msg string) {
		data.Error = msg
		data.PersonalPhrase = a.sm.GetString(ctx, phraseKey)
		a.loadPGPState(ctx, &data)
		if data.Captcha = a.newChallenge(w, r, accountPurpose); data.Captcha == nil {
			return
		}
		a.render(w, "account", http.StatusUnprocessableEntity, data)
	}
	if honeypotTriggered(r) {
		fail("submission rejected")
		return
	}
	if !a.requireFriction(w, r, accountPurpose) {
		fail("challenge failed, please try again")
		return
	}
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil {
		writeGenericError(w)
		return
	}
	if err := a.auth.AuthClearPGPKey(ctx, *uid); err != nil {
		fail("could not remove the key, please try again")
		return
	}
	// Success path renders without minting (see handlePGPUpload): the anchor
	// was consumed by requireFriction this request; a fresh mint here would
	// bind to an identifier scs may not commit.
	a.sm.Put(ctx, pgpKeySessionKey, "")
	data.PGPPublicKey = ""
	data.PGPFingerprint = ""
	data.Flash = "Public key removed."
	a.render(w, "account", http.StatusOK, data)
}

// handlePGPToolPage serves the public self-service explainer at GET /pgp:
// how to generate a key, why only PUBLIC keys are accepted, and the promise
// that the site never holds private material or decrypts anything server-side.
// Public information only — no forms, no friction required.
func (a *App) handlePGPToolPage(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	data.PersonalPhrase = a.sm.GetString(r.Context(), phraseKey)
	a.render(w, "pgp", http.StatusOK, data)
}
