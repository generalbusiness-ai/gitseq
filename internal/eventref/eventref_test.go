package eventref

import (
	"errors"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	sha1Genesis   = "5d2622748872b7e2dec3fe5c59e4be73a35e0bc8"
	sha256Genesis = "5d2622748872b7e2dec3fe5c59e4be73a35e0bc85d2622748872b7e2dec3fe5c"
)

// id builds a canonical identifier the way every surface builds one.
func id(format, genesis, hash string) string {
	return "git:" + format + ":" + genesis + "#git:" + format + ":" + hash
}

// setOf builds a verified event set from hashes, numbered from #1 as the fold
// numbers records.
func setOf(room Room, hashes ...string) Set {
	projection := workroom.Projection{}
	for index, hash := range hashes {
		projection.Decisions = append(projection.Decisions, workroom.Decision{
			Event: id(room.ObjectFormat, room.Genesis, hash), Sequence: index + 1,
		})
	}
	return FromProjection(room, projection)
}

func pad(prefix string, width int) string {
	return prefix + strings.Repeat("0", width-len(prefix))
}

// The two object formats are one rule with one number in it. A sha256 room
// names objects with 64 hexadecimal characters, and the length bound that
// decides whether a string could be part of an event hash moves with it.
func TestFormReadsBothObjectFormats(t *testing.T) {
	t.Parallel()
	sha1 := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	sha256 := Room{Genesis: sha256Genesis, ObjectFormat: "sha256"}
	fingerprint := strings.Repeat("ab", 32)
	for name, test := range map[string]struct {
		room     Room
		selector string
		want     Form
	}{
		"sha1 canonical":            {sha1, id("sha1", sha1Genesis, pad("aa", 40)), FormCanonical},
		"sha256 canonical":          {sha256, id("sha256", sha256Genesis, pad("aa", 64)), FormCanonical},
		"sha1 foreign genesis":      {sha1, id("sha1", pad("bb", 40), pad("aa", 40)), FormExternal},
		"sha256 foreign genesis":    {sha256, id("sha256", pad("bb", 64), pad("aa", 64)), FormExternal},
		"sha1 id inside sha256room": {sha256, id("sha1", sha1Genesis, pad("aa", 40)), FormExternal},
		"number":                    {sha1, "#17", FormNumber},
		"number shaped garbage":     {sha1, "#x", FormNumber},
		"short hash":                {sha1, "d54de200", FormHash},
		"whole sha1 hash":           {sha1, pad("d5", 40), FormHash},
		"fingerprint in sha1 room":  {sha1, fingerprint, FormOpaque},
		"fingerprint in sha256room": {sha256, fingerprint, FormHash},
		"uppercase hex":             {sha1, "D54DE200", FormOpaque},
		"prose":                     {sha1, "the changelog", FormOpaque},
		"batch label":               {sha1, "$retirement", FormOpaque},
		"session handle":            {sha1, "session:abcdef", FormOpaque},
		"empty":                     {sha1, "", FormOpaque},
	} {
		t.Run(name, func(t *testing.T) {
			if got := test.room.Form(test.selector); got != test.want {
				t.Fatalf("Form(%q) = %v, want %v", test.selector, got, test.want)
			}
		})
	}
}

// An actor fingerprint is 64 hexadecimal characters and a sha1 object name is
// 40, so in a sha1 workroom a fingerprint cannot be read as a hash fragment at
// all. In a sha256 workroom the two are the same width, and the guarantee is
// then the weaker but sufficient one: it is refused, never resolved to some
// event that happens to share a prefix.
func TestActorFingerprintIsNeverResolvedToAnEvent(t *testing.T) {
	t.Parallel()
	fingerprint := strings.Repeat("ab", 32)
	sha1 := setOf(Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}, pad("ab", 40))
	resolved, err := sha1.Resolve(fingerprint)
	if err != nil || resolved != fingerprint {
		t.Fatalf("sha1 room read a fingerprint as an event: %q %v", resolved, err)
	}
	sha256 := setOf(Room{Genesis: sha256Genesis, ObjectFormat: "sha256"}, pad("cd", 64))
	if _, err := sha256.Resolve(fingerprint); err == nil {
		t.Fatal("sha256 room resolved a fingerprint to an event")
	}
}

// The displays number records from #1. Every neighbour of the two ends is a
// refusal, and a refusal names the range so a reader can correct it.
func TestNumberBoundaries(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, pad("a1", 40), pad("b2", 40), pad("c3", 40))
	if got, err := set.Resolve("#1"); err != nil || got != id("sha1", sha1Genesis, pad("a1", 40)) {
		t.Fatalf("#1 = %q %v", got, err)
	}
	if got, err := set.Resolve("#3"); err != nil || got != id("sha1", sha1Genesis, pad("c3", 40)) {
		t.Fatalf("#depth = %q %v", got, err)
	}
	for _, selector := range []string{"#0", "#4", "#x", "#", "#01", "#-1", "#1.0", "# 1"} {
		got, err := set.Resolve(selector)
		var refusal *Refusal
		if !errors.As(err, &refusal) {
			t.Fatalf("Resolve(%q) = %q %v, want a refusal", selector, got, err)
		}
		if !strings.Contains(refusal.Error(), "#1 to #3") {
			t.Fatalf("Resolve(%q) refusal does not name the range: %s", selector, refusal)
		}
	}
}

// A fragment that is both a prefix and a suffix of one event hash names that
// event once. Counting the two matches separately would report a single event
// as an ambiguity, which is the refusal a caller can do nothing about.
func TestPrefixAndSuffixOfOneEventIsOneMatch(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	hash := "ab" + strings.Repeat("c", 36) + "ab"
	set := setOf(room, hash, pad("d1", 40))
	got, err := set.Resolve("ab")
	if err != nil {
		t.Fatalf("a fragment matching one event both ways was refused: %v", err)
	}
	if got != id("sha1", sha1Genesis, hash) {
		t.Fatalf("resolved to %q", got)
	}
	// The same event named by its whole hash is still one match.
	if got, err := set.Resolve(hash); err != nil || got != id("sha1", sha1Genesis, hash) {
		t.Fatalf("whole hash = %q %v", got, err)
	}
}

// A suffix is a first-class way to type a reference, because that is the half
// of an identifier a truncated display leaves visible.
func TestSuffixResolves(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, "1"+strings.Repeat("0", 31)+"deadbeef", "2"+strings.Repeat("0", 39))
	got, err := set.Resolve("deadbeef")
	if err != nil || !strings.HasSuffix(got, "deadbeef") {
		t.Fatalf("suffix = %q %v", got, err)
	}
}

// Ambiguity refuses and names the candidates as the displays name them, and
// the list is bounded: a one-character fragment in a large room is answered by
// saying how many, not by printing them all.
func TestAmbiguityRefusesAndBoundsItsCandidates(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, "a"+strings.Repeat("1", 39), "a"+strings.Repeat("2", 39), pad("b3", 40))
	_, err := set.Resolve("a")
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("an ambiguous fragment was not refused: %v", err)
	}
	if len(refusal.Candidates) != 2 {
		t.Fatalf("candidates = %+v", refusal.Candidates)
	}
	text := refusal.Error()
	for _, want := range []string{"matches 2 events", "#1 " + id("sha1", sha1Genesis, "a"+strings.Repeat("1", 39)), "#2 "} {
		if !strings.Contains(text, want) {
			t.Fatalf("refusal %q does not contain %q", text, want)
		}
	}

	hashes := make([]string, 0, MaxCandidates+3)
	for index := 0; index < MaxCandidates+3; index++ {
		hashes = append(hashes, "f"+strings.Repeat("0", 38)+string(rune('a'+index%6))+string(rune('a'+index/6)))
	}
	crowded := setOf(room, hashes...)
	_, err = crowded.Resolve("f")
	if !errors.As(err, &refusal) {
		t.Fatal("a crowded fragment was not refused")
	}
	if len(refusal.Candidates) != MaxCandidates || refusal.Omitted != 3 {
		t.Fatalf("candidates=%d omitted=%d", len(refusal.Candidates), refusal.Omitted)
	}
	if !strings.Contains(refusal.Error(), "and 3 more") {
		t.Fatalf("bounded refusal does not say how many it left out: %s", refusal)
	}
}

// Nothing matched is its own refusal, and it says where to find the right
// name rather than listing an empty set.
func TestNoMatchRefuses(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, pad("a1", 40))
	_, err := set.Resolve("beef")
	var refusal *Refusal
	if !errors.As(err, &refusal) || len(refusal.Candidates) != 0 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(refusal.Error(), "matches no event hash in this workroom") {
		t.Fatalf("refusal = %s", refusal)
	}
}

// An identifier of another workroom is preserved exactly as typed. It is a
// cross-workroom citation, not a selector, and reading it as one would let a
// number or a fragment name a different room's event by inference.
func TestForeignCanonicalIdentifierIsPreserved(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, pad("a1", 40))
	foreign := id("sha1", pad("ff", 40), pad("a1", 40))
	got, err := set.Resolve(foreign)
	if err != nil || got != foreign {
		t.Fatalf("foreign identifier = %q %v", got, err)
	}
	if set.Has(foreign) {
		t.Fatal("a foreign identifier was reported as an event this room holds")
	}
}

// The three sentences a filing surface owes an author before it signs, and the
// silence it owes on the ordinary path.
func TestBasisNotesDistinguishTheThreeCases(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, pad("a1", 40))
	live := id("sha1", sha1Genesis, pad("a1", 40))
	absent := id("sha1", sha1Genesis, pad("ee", 40))
	foreign := id("sha1", pad("ff", 40), pad("a1", 40))

	if notes := BasisNotes(set, []string{live}); len(notes) != 0 {
		t.Fatalf("a live basis was not quiet: %v", notes)
	}
	notes := BasisNotes(set, []string{"not-an-event", absent, foreign, live, foreign})
	if len(notes) != 3 {
		t.Fatalf("notes = %v", notes)
	}
	for index, want := range []string{
		`warning: rests-on "not-an-event" is not an event identifier`,
		"warning: rests-on " + absent + " names no event in this workroom",
		"note: rests-on " + foreign + " is another workroom's event",
	} {
		if !strings.HasPrefix(notes[index], want) {
			t.Fatalf("note %d = %q, want prefix %q", index, notes[index], want)
		}
	}
}

// The projection note is a list about one signed act, so it mirrors that act's
// rests_on exactly: a citation written twice is reported twice, and an empty
// string is a citation naming no event like any other. This is the shape the
// MCP adapter has always reported, and collapsing it would make the note
// disagree with the record it describes.
func TestUnresolvedMirrorsTheCitationsAsWritten(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	live := id("sha1", sha1Genesis, pad("a1", 40))
	set := setOf(room, pad("a1", 40))
	got := set.Unresolved([]string{"not-an-event", "not-an-event", "also-not", live, ""})
	want := []string{"not-an-event", "not-an-event", "also-not", ""}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Unresolved = %q, want %q", got, want)
	}
}

// A batch label names an act the same chain has yet to mint. The surfaces that
// have labels keep them out of the disclosure, and the disclosure itself says
// the same thing about the string it would otherwise be handed: it is not an
// identifier. Both halves are stated so a reader of either one is not misled.
func TestBasisNotesCallALabelWhatItIs(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, pad("a1", 40))
	if got := room.Form("$claim"); got != FormOpaque {
		t.Fatalf("Form($claim) = %v, want FormOpaque", got)
	}
	notes := BasisNotes(set, []string{"$claim"})
	if len(notes) != 1 || !strings.Contains(notes[0], "is not an event identifier") {
		t.Fatalf("notes = %v", notes)
	}
}

// The workroom's own genesis is a citation the sequencer resolves and the fold
// projects no record for. It earns no warning, it is still absent from the
// fold's own membership answer, which other surfaces report from, and no short
// reference reaches it: it has no record number, and the hash search covers
// records only.
func TestGenesisCitationIsAdmittedQuietlyAndHasNoShortForm(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	set := setOf(room, pad("a1", 40), pad("b2", 40))
	genesis := room.GenesisEvent()
	if genesis != id("sha1", sha1Genesis, sha1Genesis) {
		t.Fatalf("GenesisEvent = %q", genesis)
	}
	if !set.Resolvable(genesis) {
		t.Fatal("the sequencer resolves the genesis citation; the boundary says it does not")
	}
	if set.Has(genesis) {
		t.Fatal("the fold projects no record for the genesis; membership claims one")
	}
	if notes := BasisNotes(set, []string{genesis}); len(notes) != 0 {
		t.Fatalf("the genesis basis was warned about: %v", notes)
	}
	if _, err := set.Resolve(sha1Genesis); err == nil {
		t.Fatal("a bare genesis hash resolved to something")
	}
}

// The resolver reads the event set once, and only when something typed needs
// it. A caller who pasted canonical identifiers pays for no fold.
func TestResolverReadsTheEventSetAtMostOnceAndOnlyWhenNeeded(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	reads := 0
	resolver := New(room, func() (Set, error) {
		reads++
		return setOf(room, pad("a1", 40), pad("b2", 40)), nil
	})
	canonical := id("sha1", sha1Genesis, pad("a1", 40))
	for _, selector := range []string{canonical, "not-an-event", "$label", ""} {
		if got, err := resolver.One(selector); err != nil || got != selector {
			t.Fatalf("One(%q) = %q %v", selector, got, err)
		}
	}
	if reads != 0 {
		t.Fatalf("the event set was read %d times for references that need no resolving", reads)
	}
	if _, err := resolver.Many([]string{"#1", "#2", "b2"}); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("the event set was read %d times for one act", reads)
	}
	lines := resolver.Lines()
	if len(lines) != 3 || lines[0] != "resolved #1 -> "+canonical {
		t.Fatalf("lines = %v", lines)
	}
}

// A resolver whose event set cannot be read refuses the reference rather than
// carrying the selector through: a "#3" that reached a signed payload would be
// a citation naming nothing, permanently.
func TestResolverRefusesWhenTheEventSetCannotBeRead(t *testing.T) {
	t.Parallel()
	room := Room{Genesis: sha1Genesis, ObjectFormat: "sha1"}
	resolver := New(room, func() (Set, error) { return Set{}, errors.New("cold") })
	if _, err := resolver.One("#3"); err == nil || !strings.Contains(err.Error(), "cold") {
		t.Fatalf("err = %v", err)
	}
}
