package server

import (
"bytes"
"strings"
"testing"

"github.com/ProtonMail/go-crypto/openpgp"
)

func TestProbeSerializeOutput(t *testing.T) {
ent, err := openpgp.NewEntity("Probe", "", "p@e.com", nil)
if err != nil {
t.Fatal(err)
}
var pub bytes.Buffer
if err := ent.Serialize(&pub); err != nil {
t.Fatal(err)
}
s := pub.String()
t.Logf("PUB starts with %q len=%d", s[:40], len(s))
var priv bytes.Buffer
if err := ent.SerializePrivate(&priv, nil); err != nil {
t.Fatal(err)
}
t.Logf("PRIV starts with %q", strings.ReplaceAll(priv.String()[:45], "\n", "\\n"))
}
