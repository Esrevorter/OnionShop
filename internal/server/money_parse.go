package server

import (
	"strconv"
	"fmt"
	"strings"
)

// parseDecimalUnits converts a decimal string to integer minor units EXACTLY.
// No float anywhere in the money path (TASKS.md §10.4). Rejects zero, junk,
// excess precision and int64 overflow. decimals=2 → cents, 12 → piconero.
func parseDecimalUnits(s string, decimals int) (int64, error) {
	return parseDecimal(s, decimals, false)
}

// parseDecimalUnitsAllowZero is for settings where 0 is meaningful
// (e.g. vendor registration fee = disabled).
func parseDecimalUnitsAllowZero(s string, decimals int) (int64, error) {
	return parseDecimal(s, decimals, true)
}

func parseDecimal(s string, decimals int, allowZero bool) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" { return 0, fmt.Errorf("empty amount") }
	if strings.HasPrefix(s, "-") { return 0, fmt.Errorf("negative amount") }
	whole, frac := s, ""
	if i := strings.IndexAny(s, ".,"); i >= 0 {
		whole, frac = s[:i], s[i+1:]
		if strings.ContainsAny(frac, ".,") { return 0, fmt.Errorf("malformed amount") }
	}
	if whole == "" { whole = "0" }
	for _, c := range whole + frac {
		if c < '0' || c > '9' { return 0, fmt.Errorf("malformed amount") }
	}
	if len(frac) > decimals { return 0, fmt.Errorf("too many decimal places (max %d)", decimals) }
	frac += strings.Repeat("0", decimals-len(frac))
	var scale int64 = 1
	for i := 0; i < decimals; i++ { scale *= 10 }
	const maxI = int64(9223372036854775807)
	var w int64
	for _, c := range whole {
		d := int64(c - '0')
		if w > (maxI-d)/10 { return 0, fmt.Errorf("amount too large") }
		w = w*10 + d
	}
	if w > maxI/scale { return 0, fmt.Errorf("amount too large") }
	var fr int64
	for _, c := range frac { fr = fr*10 + int64(c-'0') }
	if fr > maxI-w*scale { return 0, fmt.Errorf("amount too large") }
	total := w*scale + fr
	if total == 0 && !allowZero { return 0, fmt.Errorf("amount must be positive") }
	return total, nil
}

// formatPiconero renders integer piconero as a trimmed native-XMR string.
// Integer arithmetic only: no float anywhere in the money path.
func formatPiconero(pico int64) string {
	neg := ""
	if pico < 0 { neg, pico = "-", -pico }
	micro := (pico + 500000) / 1000000
	whole, frac := micro/1000000, micro%1000000
	out := neg + strconv.FormatInt(whole, 10)
	if frac > 0 {
		fs := strconv.FormatInt(1000000+frac, 10)[1:]
		for len(fs) > 0 && fs[len(fs)-1] == '0' { fs = fs[:len(fs)-1] }
		out += "." + fs
	}
	return out
}
