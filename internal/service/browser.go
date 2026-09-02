package service

import "net/http"

// browserPolicy is what every response from this service tells a browser, in
// one place so a later endpoint inherits it instead of remembering it.
//
// Framing is denied twice: frame-ancestors is the modern directive, and
// X-Frame-Options covers agents that predate it. The Content-Security-Policy
// admits only the embedded bundle's own origin for scripts, styles, fonts,
// images and fetches, which is all the UI uses: index.html carries one module
// script and one stylesheet, the fonts and the icon are bundled assets, and
// the page talks only to its own origin. Nothing may be embedded, navigated to
// through a base element, or submitted through a form. nosniff keeps a
// browser from second-guessing the content types the file server declares,
// which module scripts already require, and the referrer never leaves.
//
// style-src 'self' refuses style attributes and style elements that arrive in
// markup or through setAttribute, and any stylesheet from another origin. It
// does not govern the CSSOM, which is how React applies a component's style
// prop, so the UI's few inline styles render under this policy. A real
// browser driven against a served build reported no policy violations.
const (
	contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
		"img-src 'self'; font-src 'self'; connect-src 'self'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'"
	frameOptions       = "DENY"
	contentTypeOptions = "nosniff"
	referrerPolicy     = "no-referrer"
)

// BrowserHeaders applies the browser policy before the wrapped handler runs,
// so the headers reach UI pages, assets, JSON views, refusals and not-found
// answers alike, whatever the handler then sets or writes. Server.Handler
// applies it, and the resident command applies it again outermost so the
// refusals composed around the server, such as the trusted-host check, carry
// it too; setting the same values twice is harmless.
func BrowserHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := writer.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("X-Frame-Options", frameOptions)
		header.Set("X-Content-Type-Options", contentTypeOptions)
		header.Set("Referrer-Policy", referrerPolicy)
		next.ServeHTTP(writer, request)
	})
}
