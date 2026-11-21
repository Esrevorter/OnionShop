package server

import (
	"net/http"
	"strconv"
)

// genericErrorRenderer is set after templates build; writeGenericError
// dispatches to it so panics surface the branded page. The plain-text fallback
// (nil renderer) is pinned by TestRecoverMiddlewareGeneric500.
var genericErrorRenderer func(w http.ResponseWriter)

// installGenericErrorRenderer is called once templates are built so panics
// surface the branded page_error.html instead of plain text.
func installGenericErrorRenderer(r func(w http.ResponseWriter)) {
	genericErrorRenderer = r
}

// renderStaticError is the dispatch target installed after templates build.
// It reuses the existing page_error.html (which handles 403/404/405/500) with
// an empty AuthData so the branded layout renders without a request context.
func (a *App) renderStaticError(w http.ResponseWriter, code int, msg string) {
	tpl := a.templates["error"]
	if tpl == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := struct {
		AuthData
		ErrorCode    string
		ErrorMessage string
	}{AuthData: AuthData{}, ErrorCode: strconv.Itoa(code), ErrorMessage: msg}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = tpl.ExecuteTemplate(w, "layout", data)
}
