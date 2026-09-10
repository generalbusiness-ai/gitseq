package workroom

// The lifecycle words a commitment's Status carries, in one place, read two
// ways.
//
// Two readers outside this package already needed this vocabulary and each
// kept its own copy of the words, with opposite defaults. The merge plan asks
// whether a commitment is still open and treats a word it has never heard of
// as closed, so an unrecognised status frees an artifact rather than
// protecting it forever. The checkout classification asks whether one is
// closed and treats an unfamiliar word as open, so an unrecognised status
// protects a checkout rather than offering it for deletion. Both defaults are
// right where they sit, and making the two agree would break one of them.
//
// What was worth fixing is the two copies of the words themselves. A status
// added to one list and not the other makes the same repository answer two
// ways about the same commitment, and nothing would have said so. So the
// words live here, beside the field they describe, and the two readings stay
// two.
//
// Staleness is not settlement. "stale" is on the unsettled list: a stale
// commitment is one whose reasoning moved, and its work is still owed.

// unsettledStatuses are the words that say a commitment is still open.
var unsettledStatuses = map[string]bool{
	"open":                   true,
	"promised":               true,
	"reported":               true,
	"awaiting-review":        true,
	"awaiting-authorization": true,
	"awaiting-landing":       true,
	"stale":                  true,
}

// settledStatuses are the words that say a commitment closed.
var settledStatuses = map[string]bool{
	"satisfied":  true,
	"abandoned":  true,
	"superseded": true,
	"withdrawn":  true,
	"cancelled":  true,
	"reneged":    true,
}

// UnsettledCommitment reports whether a status word says this commitment is
// still open. A word this vocabulary does not know answers false, which reads
// as settled: the callers of this direction warn or free something, and a
// warning that never stops for an unfamiliar word is the safe way to be
// wrong.
func UnsettledCommitment(status string) bool { return unsettledStatuses[status] }

// SettledCommitment reports whether a status word says this commitment
// closed. A word this vocabulary does not know answers false, which reads as
// unsettled: the caller of this direction decides whether a checkout may be
// deleted, and a status it cannot settle is not one it may delete on.
func SettledCommitment(status string) bool { return settledStatuses[status] }
