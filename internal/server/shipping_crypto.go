package server

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// encryptToVendor encrypts the buyer's delivery instructions to the VENDOR's
// stored PUBLIC key. The server never holds private material nor decrypts.
func encryptToVendor(ctx context.Context, pool *pgxpool.Pool, vendorUserID pgtype.UUID, plaintext string) (string, error) {
	var armored string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(pgp_public_key,'') FROM users WHERE id=$1`, vendorUserID).Scan(&armored); err != nil {
		return "", err
	}
	if armored == "" {
		return "", fmt.Errorf("vendor has no PGP public key")
	}
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil || len(ring) == 0 {
		return "", fmt.Errorf("vendor PGP key unreadable")
	}
	var buf bytes.Buffer
	aw, err := armor.Encode(&buf, "PGP MESSAGE", nil)
	if err != nil { return "", err }
	w, err := openpgp.Encrypt(aw, ring, nil, nil, nil)
	if err != nil { return "", err }
	if _, err := w.Write([]byte(plaintext)); err != nil { return "", err }
	if err := w.Close(); err != nil { return "", err }
	if err := aw.Close(); err != nil { return "", err }
	return buf.String(), nil
}
