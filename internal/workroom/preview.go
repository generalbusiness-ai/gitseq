package workroom

// Preview answers what this fold would decide about one record if it were
// appended now. It is the fold's own judgement, made by the same rules the log
// is read with — not a second, friendlier copy of them that can drift. A write
// boundary uses it to refuse an act before signing it, with the reason the log
// would otherwise have recorded forever.
//
// The answer is about the world as this folder stands. Anything that lands
// between the preview and the append changes it, so the preview is advice and
// the fold at sequencing is the decision. A caller that cannot fold cheaply, or
// that is about to submit through a resident whose frontier it cannot see,
// should treat a preview it could not make as no objection at all.
//
// The record is prospective: it has no identifier yet, so the caller passes one
// no event holds. A duplicate is answered as a duplicate, which is what the log
// would say too.
//
// Nothing a later reader can see changes. The decision path writes in two
// places: the interned string pool, which is a memo of values that carries no
// meaning, and the admitted-claim index, which is keyed by this record's own
// identifier and is restored here exactly as it was found.
func (f *Folder) Preview(record Record) Decision {
	return f.state.preview(record)
}

func (f *foldState) preview(record Record) Decision {
	beyondSeam := f.beyondSeam
	claim, claimed := f.admittedClaims[record.ID]
	_, _, decision, _ := f.judge(len(f.records), record)
	f.beyondSeam = beyondSeam
	if claimed {
		f.admittedClaims[record.ID] = claim
	} else {
		delete(f.admittedClaims, record.ID)
	}
	return decision
}
