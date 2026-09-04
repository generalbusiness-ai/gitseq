package statusview

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// hostileText is what an actor can write into any durable or live string: a
// screen clear, a bell, a carriage return that repaints the line already
// printed, a right-to-left override that makes the rest read backwards, an
// invisible joiner, a C1 control introducer, and a byte that is not UTF-8.
const hostileText = "before\x1b[2Jafter\x07\rrepaint‮reversed‍hidden\x9bcsi\xffraw"

// forbidden reports every rune a terminal must never receive from untrusted
// text: the C0 controls including newline and tab, DEL, the C1 range, the
// invisible format characters, and the line and paragraph separators. Newline
// is on the list because a line an attacker adds looks like a line the program
// wrote.
func forbidden(value rune) bool {
	switch {
	case value < 0x20 || value == 0x7f || (value >= 0x80 && value <= 0x9f):
		return true
	case unicode.Is(unicode.Cf, value) || value == '\u2028' || value == '\u2029':
		return true
	case value == utf8.RuneError:
		return true
	}
	return false
}

func firstHostile(value string) (rune, bool) {
	for _, decoded := range value {
		if forbidden(decoded) {
			return decoded, true
		}
	}
	return 0, false
}

func TestTextEscapesEveryControlThatCanDriveATerminal(t *testing.T) {
	got := Text(hostileText)
	if found, bad := firstHostile(got); bad {
		t.Fatalf("Text passed %U through: %q", found, got)
	}
	for _, want := range []string{`\x1b`, `\x07`, `\u202e`, `\u200d`, `\x9b`, `\xff`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Text lost the evidence for %s: %q", want, got)
		}
	}
	// Carriage return is whitespace, so the one-line fold turns it into a
	// separator rather than an escape; it cannot repaint a line it is not in.
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("Text kept a carriage return: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "reversed") {
		t.Fatalf("Text dropped legible content: %q", got)
	}
}

func TestTextFoldsBenignSpacingAndLeavesOrdinaryTextAlone(t *testing.T) {
	if got := Text("  two   words  "); got != "two words" {
		t.Fatalf("benign spacing folding changed: %q", got)
	}
	ordinary := "Ada wrote docs/reference/architecture.md and 界 is fine"
	if got := Text(ordinary); got != ordinary {
		t.Fatalf("ordinary text was rewritten: %q", got)
	}
}

// A structural character folded to a space is safe but silent: the reader
// cannot tell a line break from a space somebody typed. The request asks for
// visible and unambiguous, so Text escapes these rather than folding them.
func TestTextShowsWhichStructuralCharacterWasThere(t *testing.T) {
	for _, item := range []struct {
		name  string
		input string
		want  string
	}{
		{"line feed", "a\nb", `a\x0ab`},
		{"carriage return", "a\rb", `a\x0db`},
		{"tab", "a\tb", `a\x09b`},
		{"line separator", "a\u2028b", `a\u2028b`},
		{"paragraph separator", "a\u2029b", `a\u2029b`},
		{"next line", "a\u0085b", `a\x85b`},
	} {
		if got := Text(item.input); got != item.want {
			t.Errorf("%s: Text(%q) = %q, want %q", item.name, item.input, got, item.want)
		}
	}
	if got := Text("a b"); got != "a b" {
		t.Errorf("an ordinary space should still be a space: %q", got)
	}
}

func TestTextCapNeverSplitsAnEscape(t *testing.T) {
	got := Text(strings.Repeat("\x1b", TextCap))
	if len(got) > TextCap {
		t.Fatalf("escaped text ran past the cap: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated text lost its mark: %q", got)
	}
	body := strings.TrimSuffix(got, "…")
	if len(body)%4 != 0 || strings.Count(body, `\x1b`)*4 != len(body) {
		t.Fatalf("truncation split an escape: %q", body)
	}
}

func TestSafeEscapesStructuralWhitespaceSoTextCannotInventALine(t *testing.T) {
	whole := "first line\n\tindented\rrepaint\x1b[31mred\u2028separated"
	got := Safe(whole)
	if found, bad := firstHostile(got); bad {
		t.Fatalf("Safe passed %U through: %q", found, got)
	}
	for _, want := range []string{`\x0a`, `\x09`, `\x0d`, `\x1b`, `\u2028`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Safe kept %s as itself: %q", want, got)
		}
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("Safe left a real line break in untrusted text: %q", got)
	}
	if !strings.Contains(got, "first line") || !strings.Contains(got, "separated") {
		t.Fatalf("Safe dropped legible content: %q", got)
	}
}

func TestSafeKeepsLengthAndSpacingThatTextWouldFold(t *testing.T) {
	long := strings.Repeat("paragraph  text ", 200)
	if Safe(long) != long {
		t.Fatal("Safe truncated or folded text it must render whole")
	}
	if Text(long) == long {
		t.Fatal("Text should have folded the double spaces and capped the length")
	}
}

// A rune above U+FFFF cannot be written as \uNNNN: the escape would run to
// five hex digits and a hex digit after it would be indistinguishable from
// part of the escape.
func TestEscapesAboveTheBasicPlaneAreFixedWidth(t *testing.T) {
	const languageTag = '\U000e0001' // a format character, so it is escaped
	for name, got := range map[string]string{
		"Text": Text(string(languageTag) + "abcdef"),
		"Safe": Safe(string(languageTag) + "abcdef"),
	} {
		if !strings.HasPrefix(got, `\U000e0001abcdef`) {
			t.Fatalf("%s wrote an ambiguous escape: %q", name, got)
		}
	}
	if got := Safe("\ufeffx"); got != `\ufeffx` {
		t.Fatalf("a BMP format character should stay four wide: %q", got)
	}
	if got := Safe("\x1bx"); got != `\x1bx` {
		t.Fatalf("a byte-range control should stay two wide: %q", got)
	}
}

// hostileSnapshot puts hostileText in every string an actor controls, so the
// walk below fails for any rendering path that forgets to neutralize one.
func hostileSnapshot() (app.Snapshot, nexus.Snapshot, *nexus.Inbox) {
	projection := workroom.Projection{Actors: map[string]workroom.ActorState{
		"attacker": {Name: hostileText, Kind: hostileText, MembershipEvent: "membership", Roles: []string{hostileText}},
		"reader":   {Name: "Ada"},
	}}
	projection.Statements = append(projection.Statements,
		workroom.Statement{Event: "request", Actor: "attacker", Kind: workroom.KindRequest, Text: hostileText,
			Body: map[string]string{"conditions": hostileText}},
		workroom.Statement{Event: "report", Actor: "attacker", Kind: workroom.KindReport, Text: hostileText},
		workroom.Statement{Event: "refused", Actor: "attacker", Kind: workroom.KindAssert, Text: hostileText},
	)
	projection.Commitments = append(projection.Commitments, workroom.Commitment{
		Request: "request", Requester: "attacker", Performer: "reader", WaitingOn: "reader",
		AddressedTo: "attacker", Status: "open",
	})
	projection.Artifacts = append(projection.Artifacts, workroom.Artifact{
		Event: "artifact", Path: hostileText, Commit: "commit", Stale: true,
		StaleBecause: hostileText, StaleBecausePath: hostileText,
	})
	projection.Decisions = append(projection.Decisions,
		workroom.Decision{Event: "request", Verdict: workroom.Effective, Reason: hostileText},
		workroom.Decision{Event: "refused", Verdict: workroom.Ineffective, Reason: hostileText},
	)
	live := nexus.Snapshot{
		Presence: map[string]string{"session": hostileText},
		Activity: map[string]nexus.Activity{"session": {Status: nexus.ActivityBusy, Note: hostileText, Focus: []string{hostileText}}},
	}
	inbox := &nexus.Inbox{Frames: []nexus.InboxFrame{{
		Actor: "attacker", Text: hostileText, About: "request", Conversation: "conversation", Thread: "thread",
	}}}
	return app.Snapshot{Genesis: "genesis", Head: "head", Depth: 2, Projection: projection}, live, inbox
}

// walk reports the first hostile rune reachable through any exported string in
// a built view, naming the field so a failure says which caller forgot.
func walk(t *testing.T, path string, value reflect.Value) {
	t.Helper()
	switch value.Kind() {
	case reflect.String:
		if found, bad := firstHostile(value.String()); bad {
			t.Errorf("%s carries %U unescaped: %q", path, found, value.String())
		}
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			walk(t, path, value.Elem())
		}
	case reflect.Slice, reflect.Array:
		for index := range value.Len() {
			walk(t, fmt.Sprintf("%s[%d]", path, index), value.Index(index))
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			walk(t, fmt.Sprintf("%s[%v]", path, key), value.MapIndex(key))
		}
	case reflect.Struct:
		for index := range value.NumField() {
			field := value.Type().Field(index)
			if field.PkgPath != "" {
				continue
			}
			walk(t, path+"."+field.Name, value.Field(index))
		}
	}
}

func TestEveryBoundedViewNeutralizesHostileText(t *testing.T) {
	durable, live, inbox := hostileSnapshot()
	cursor := Cursor{Frontier: []Frontier{{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth}}}

	work, err := BuildWorkPage(durable, WorkQuery{Actor: "attacker"}, false)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := BuildArtifactPage(durable, ArtifactQuery{Paths: []string{hostileText}}, false)
	if err != nil {
		t.Fatal(err)
	}
	reviews, err := BuildReviewGate(durable, ListCap, false)
	if err != nil {
		t.Fatal(err)
	}
	staleness, err := BuildStalenessWave(durable, hostileText, false)
	if err != nil {
		t.Fatal(err)
	}
	orientation, _ := BuildOrientation(durable, "attacker", hostileText)

	views := map[string]any{
		"summary":     Build(durable.Genesis, durable.Head, durable.Depth, durable.Projection),
		"actor":       BuildActorStatus(durable, live, cursor, inbox, "attacker", hostileText, false),
		"wait":        BuildWait(durable, cursor, nil, false, cursor, inbox, "attacker", hostileText, false),
		"orientation": orientation,
		"work":        work,
		"artifacts":   artifacts,
		"reviews":     reviews,
		"staleness":   staleness,
	}
	for name, view := range views {
		walk(t, name, reflect.ValueOf(view))
	}
}

func TestRenderedTextCarriesNoTerminalControl(t *testing.T) {
	durable, _, _ := hostileSnapshot()
	rendered := string(Render(Build(durable.Genesis, durable.Head, durable.Depth, durable.Projection), "test"))
	for _, line := range strings.Split(rendered, "\n") {
		if found, bad := firstHostile(line); bad {
			t.Fatalf("rendered line carries %U: %q", found, line)
		}
	}
}
