package server

import (
	"bytes"
	"log"
	"net/http"
)

// renderCheckout renders a page with the additional checkout payload while
// leaving the AuthData renderer used by all other page handlers unchanged.
func (a *App) renderCheckout(w http.ResponseWriter, name string, code int, data CheckoutData) {
	tpl := a.templates[name]
	if tpl == nil {
		writeGenericError(w)
		return
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("server: render %q failed: %v", name, err)
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}
