package identity

import "fmt"

// Display returns a compact plain-text identity that keeps both trust axes
// visible. An unanchored key is named honestly by its actor fingerprint. A
// handle is presentation only; the scheme and stable subject remain visible
// beside it and remain the identity applications compare.
func (r Resolved) Display(actor string) string {
	if !r.Anchored {
		return actor + " (unanchored)"
	}
	identity := r.Identity.Scheme + ":" + r.Identity.Subject
	if r.Identity.Handle != "" {
		identity = r.Identity.Handle + " [" + identity + "]"
	}
	return fmt.Sprintf("%s (%s; %s)", identity, r.Vouching, r.Verification)
}
