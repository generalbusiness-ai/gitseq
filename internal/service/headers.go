package service

import "net/http"

// Browser protections for everything this resident serves.
//
// The resident answers on loopback only, which bounds who can reach it but
// not what a page already open in the operator's browser may do to it. These
// four headers close the gaps that remain: another origin framing the view to
// trick a click, a browser guessing a content type the server did not send,
// and the served repository path leaking outward in a Referer.
//
// They are set in one wrapper rather than per handler, and set before any
// handler runs, so no route can omit them and no early return can skip them.
// That is a structural guarantee rather than a convention: a handler writes
// its own headers after this one has already written these, and a header map
// written before the first WriteHeader is what reaches the client.
//
// This deliberately does not attempt a full document CSP. frame-ancestors is
// the directive that answers the framing risk and can be asserted without a
// browser; script-src and style-src for the embedded UI cannot be verified
// here, and a policy that silently breaks the shipped page would be worse
// than the gap it closes.
var browserProtections = map[string]string{
	// No origin may frame this view, so a click on it cannot be borrowed.
	// frame-ancestors is the modern spelling and the one browsers honour.
	"Content-Security-Policy": "frame-ancestors 'none'",
	// The same rule for anything that still reads only the legacy header.
	"X-Frame-Options": "DENY",
	// Serve types honestly and have them believed: no sniffing an asset into
	// a script.
	"X-Content-Type-Options": "nosniff",
	// The URL of a loopback page names the host and nothing outward needs it.
	"Referrer-Policy": "no-referrer",
}

// secureHeaders applies browserProtections to every response.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := writer.Header()
		for name, value := range browserProtections {
			header.Set(name, value)
		}
		next.ServeHTTP(writer, request)
	})
}
