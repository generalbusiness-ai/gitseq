package reviewguard

import (
	"errors"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// ReadFunc returns one guarded basis read: the basis and its news judged from
// one verified snapshot, plus that snapshot's projection for exact-set
// validation. Every filing surface supplies its own read — the command line
// validates a checkout, the tool takes the head from the artifact row — and
// this package owns everything both do after reading.
type ReadFunc func() (Basis, []News, workroom.Projection, error)

// Confirm runs the guarded-review confirmation choreography every filing
// surface shares, exactly once per verdict: an initial read with exact-set
// validation of the cited artifacts, an immediate re-read that must agree
// with it, and one last confirming read whose frontier the signed act binds
// to. Movement between reads refuses before anything is built, so a statement
// landing while the reviewer was working is seen rather than signed over;
// acknowledgment validation then holds the supplied set to the news the
// confirming read observed beyond the verdict's own citations. The returned
// body and causal references are what Build would produce at that read.
func Confirm(read ReadFunc, citations, acknowledgments []string, verdict, text string) (map[string]string, []string, error) {
	basis, news, projection, err := read()
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateSet(projection, basis.Head, citations); err != nil {
		return nil, nil, err
	}
	repeatedBasis, repeatedNews, repeatedProjection, err := read()
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateSet(repeatedProjection, repeatedBasis.Head, citations); err != nil {
		return nil, nil, err
	}
	if !SameRead(basis, news, repeatedBasis, repeatedNews) {
		return nil, nil, errors.New("review basis changed while validating; rerun and acknowledge any head news it names")
	}
	confirmedBasis, confirmedNews, confirmedProjection, err := read()
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateSet(confirmedProjection, confirmedBasis.Head, citations); err != nil {
		return nil, nil, err
	}
	if !SameRead(repeatedBasis, repeatedNews, confirmedBasis, confirmedNews) {
		return nil, nil, errors.New("review basis changed before signing; rerun and acknowledge any head news it names")
	}
	plannedCitations := append([]string{confirmedBasis.Promise, confirmedBasis.Request}, citations...)
	if err := ValidateAcknowledgments(confirmedNews, plannedCitations, acknowledgments); err != nil {
		return nil, nil, err
	}
	return Build(confirmedBasis, verdict, text, citations, confirmedNews)
}
