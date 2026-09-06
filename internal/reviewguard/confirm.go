package reviewguard

import (
	"errors"
	"fmt"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// ReadFunc returns one guarded basis read: the basis and its news judged from
// one verified snapshot, plus that snapshot's projection for exact-set
// validation. Every filing surface supplies its own read — the command line
// validates a checkout, the tool takes the head from the artifact row — and
// this package owns everything both do after reading.
type ReadFunc func() (Basis, []News, workroom.Projection, error)

// Selection is the reviewer's explicit word about what the examined set is:
// the implementation lifecycles a combined candidate closes, the adopted
// decision a self-initiated primary rests on, or that the primary is evidence
// against a request owing no Git artifact. An empty selection is the ordinary
// assigned review: the primary is bound by exact report equality.
type Selection struct {
	Implementations []string
	Decision        string
	EvidenceOnly    bool
}

func (s Selection) scope(head string, citations []string) Scope {
	return Scope{Candidate: head, Examined: citations, Implementations: s.Implementations, Decision: s.Decision, EvidenceOnly: s.EvidenceOnly}
}

// Confirm is ConfirmSelection with the ordinary assigned selection.
func Confirm(read ReadFunc, citations, acknowledgments []string, verdict, text string) (map[string]string, []string, error) {
	return ConfirmSelection(read, Selection{}, citations, acknowledgments, verdict, text)
}

// ConfirmSelection runs the guarded-review confirmation choreography every
// filing surface shares, exactly once per verdict: an initial read with
// exact-set validation of the cited artifacts and binding resolution, an
// immediate re-read that must agree with it, and one last confirming read
// whose frontier the signed act binds to. Movement between reads refuses
// before anything is built, so a statement landing while the reviewer was
// working is seen rather than signed over, and so is a reporting link,
// target or decision that moved. Acknowledgment validation then holds the
// supplied set to the news the confirming read observed beyond the verdict's
// own citations. The returned body and causal references are what Build
// would produce at that read, carrying the binding the confirming read
// resolved.
func ConfirmSelection(read ReadFunc, selection Selection, citations, acknowledgments []string, verdict, text string) (map[string]string, []string, error) {
	basis, news, binding, err := readBound(read, selection, citations)
	if err != nil {
		return nil, nil, err
	}
	repeatedBasis, repeatedNews, repeatedBinding, err := readBound(read, selection, citations)
	if err != nil {
		return nil, nil, err
	}
	if !SameRead(basis, news, repeatedBasis, repeatedNews) {
		return nil, nil, errors.New("review basis changed while validating; rerun and acknowledge any head news it names")
	}
	if !SameBinding(binding, repeatedBinding) {
		return nil, nil, errors.New("implementation binding changed while validating; rerun the review")
	}
	confirmedBasis, confirmedNews, confirmedBinding, err := readBound(read, selection, citations)
	if err != nil {
		return nil, nil, err
	}
	if !SameRead(repeatedBasis, repeatedNews, confirmedBasis, confirmedNews) {
		return nil, nil, errors.New("review basis changed before signing; rerun and acknowledge any head news it names")
	}
	if !SameBinding(repeatedBinding, confirmedBinding) {
		return nil, nil, errors.New("implementation binding changed before signing; rerun the review")
	}
	plannedCitations := append([]string{confirmedBasis.Promise, confirmedBasis.Request}, citations...)
	if err := ValidateAcknowledgments(confirmedNews, plannedCitations, acknowledgments); err != nil {
		return nil, nil, err
	}
	return BuildBound(confirmedBasis, verdict, text, citations, confirmedNews, confirmedBinding)
}

// Prepare is the read-only form: one guarded read, exact-set validation and
// binding resolution, and nothing else. It returns the binding and its
// explanation for the reviewer to correct their inputs against. It signs
// nothing, reserves nothing, and the ordinary filing re-resolves everything
// whether or not this ran.
func Prepare(read ReadFunc, selection Selection, citations []string) (Binding, string, error) {
	_, _, binding, projection, err := readBoundProjection(read, selection, citations)
	if err != nil {
		return Binding{}, "", err
	}
	return binding, Explain(binding, projection), nil
}

// readBound is one guarded read followed by the two checks every read makes:
// the cited set stands at the head, and the set resolves to one binding.
func readBound(read ReadFunc, selection Selection, citations []string) (Basis, []News, Binding, error) {
	basis, news, binding, _, err := readBoundProjection(read, selection, citations)
	return basis, news, binding, err
}

func readBoundProjection(read ReadFunc, selection Selection, citations []string) (Basis, []News, Binding, workroom.Projection, error) {
	basis, news, projection, err := read()
	if err != nil {
		return Basis{}, nil, Binding{}, workroom.Projection{}, err
	}
	if err := ValidateSet(projection, basis.Head, citations); err != nil {
		return Basis{}, nil, Binding{}, workroom.Projection{}, err
	}
	binding, err := Resolve(projection, selection.scope(basis.Head, citations))
	if err != nil {
		return Basis{}, nil, Binding{}, workroom.Projection{}, fmt.Errorf("implementation binding: %w", err)
	}
	return basis, news, binding, projection, nil
}
