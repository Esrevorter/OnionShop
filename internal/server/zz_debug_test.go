package server

import (
"bytes"
"strings"
"testing"

"github.com/ProtonMail/go-crypto/openpgp"
)

func TestZZDebugSerialize(t *testing.T) {
pub, _ := generateArmoredKeyPair(t)
ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(pub))
if err != nil { t.Fatal(err) }
e := ring[0]
t.Logf("input identities: %d", len(e.Identities))
var bin bytes.Buffer
if err := e.Serialize(&bin); err != nil { t.Fatal(err) }
t.Logf("serialized bin len: %d", bin.Len())
got, err := ValidatePGPPublicKeyArmor(pub)
if err != nil { t.Fatal(err) }
ring2, err2 := openpgp.ReadArmoredKeyRing(strings.NewReader(got))
t.Logf("reparse err: %v", err2)
if err2 == nil {
for k := range ring2[0].Identities { t.Logf("canonical identity: %q", k) }
}
// also parse raw bin directly
ring3, err3 := openpgp.ReadKeyRing(bytes.NewReader(bin.Bytes()))
t.Logf("rawbin readkeyring err: %v", err3)
if err3 == nil { for k := range ring3[0].Identities { t.Logf("rawbin identity: %q", k) } }
}
