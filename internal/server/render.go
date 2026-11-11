func (a *App) render(w http.ResponseWriter, name string, code int, data any) {
	tpl := a.templates[name]
	if tpl == nil {
		writeGenericError(w)
		return
	}
	// Buffer first: an execution error after headers are out is unrecoverable
	// and would leave the client with a truncated 200. Rendering into memory
	// lets a failure still answer the mandated generic 500 (these pages are
	// small by design — no streaming bodies in Phase 3).
	var buf bytes.Buffer
	// Each stored set was parsed as template.New("page_x").ParseFiles(layout,
	// partials..., page). Because the page file starts with
	// {{template "layout" .}} and only DEFINES "title"/"body", the page's own
	// content lives inside the "layout" template once parsed; layout invokes
	// those blocks at execution time. So execute "layout" — NOT the short name
	// ("login" is undefined inside the set) and NOT the raw page root (it is
	// an empty shell whose only statement is the layout call). The debug log
	// keeps the RAW error text: template errors never contain user secrets,
	// and sanitizedErr() here previously disguised every render failure as
	// "redis: operation failed".
	if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("server: render %q failed: %v", name, err)
		writeGenericError(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}
