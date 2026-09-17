package main

import (
	"flag"
	"fmt"
	"strings"
)

// commandNames is the list `gs` prints with no subcommand. It is also what
// every worked example below is checked against, so a command added here
// without an example is a test failure rather than a silent gap.
const commandNames = "init, actor-add, actor-retire, role-grant, role-revoke, actors, whoami, state, promise, artifact, review-request, review, land, merge, merge-plan, ratify, supersede, reassign-if-unclaimed, batch, publish, status, work, artifacts, supersession-plan, staleness-wave, inspect, reviews, provenance, verify, checkpoint-clear, serve, attach"

// commandExamples is one worked invocation per command: the shortest line that
// works, in the shape of that command's reference page under docs/reference/gs/.
// A person who mistyped a command needs one correct call more than a second copy
// of the flag list they just failed to use. Short references stand in for event
// ids, which are too long to read in an error.
var commandExamples = map[string]string{
	"init":                  `gs init --repo . --operator alice`,
	"actor-add":             `gs actor-add --as alice --name bot --kind agent`,
	"actor-retire":          "gs actor-retire --as alice --actor bot",
	"role-grant":            `gs role-grant --as alice --actor bot --role ratifier`,
	"role-revoke":           `gs role-revoke --as alice --actor bot --role ratifier`,
	"actors":                `gs actors`,
	"whoami":                `gs whoami --as alice`,
	"state":                 `gs state --as bot --kind promise --text 'I will do it' --rests-on '#42'`,
	"promise":               `gs promise --as bot '#42'`,
	"artifact":              `gs artifact --as bot --head 1f0c9ab... --promise '#49' docs/reference/gs/state.md`,
	"review-request":        `gs review-request --as bot --head 1f0c9ab... --to reviewer`,
	"land":                  `gs land --as bot --approval '#57' --checkout . --text 'what landed and why it matters'`,
	"review":                `gs review --as reviewer --checkout ../feature --artifact '#51' --promise '#49' --verdict approved --text 'approved at that head'`,
	"merge":                 `gs merge --as bot --checkout . --candidate 1f0c9ab... --approval '#57'`,
	"merge-plan":            `gs merge-plan --checkout . --candidate 1f0c9ab... --approval '#57'`,
	"ratify":                `gs ratify --as alice '#57'`,
	"supersede":             `gs supersede --as alice --text 'withdrawn' '#42'`,
	"reassign-if-unclaimed": `gs reassign-if-unclaimed --as alice --to bot --text 'please take this' --conditions 'the tests pass' --idempotency-key reassign-42 '#42'`,
	"batch":                 `gs batch --as alice chain.json`,
	"publish":               `gs publish --as alice --basis '#42'`,
	"status":                `gs status`,
	"work":                  `gs work --as bot`,
	"artifacts":             `gs artifacts`,
	"supersession-plan":     `gs supersession-plan --as alice --path docs/reference/gs/state.md --text 'superseded by the new page'`,
	"staleness-wave":        "gs staleness-wave --path docs/reference/gs/state.md",
	"inspect":               `gs inspect '#42'`,
	"reviews":               `gs reviews`,
	"provenance":            `gs provenance '#57'`,
	"verify":                `gs verify`,
	"checkpoint-clear":      `gs checkpoint-clear`,
	"serve":                 `gs serve --listen 127.0.0.1:7777`,
	"attach":                "gs attach --remote origin --genesis 1f0c9ab...",
}

// positionalSubjects names, per command, the flags a caller reaches for when
// that command takes its subject as a positional argument. Guessing `--target`
// where `<target-event>` belongs is the commonest malformed invocation here, and
// "flag provided but not defined" never says where the value should have gone.
var positionalSubjects = map[string][]string{
	"promise":               {"request", "target", "event"},
	"artifact":              {"path", "paths"},
	"ratify":                {"target", "event", "statement", "report"},
	"supersede":             {"target", "event", "statement"},
	"inspect":               {"event", "target"},
	"provenance":            {"event", "target"},
	"reassign-if-unclaimed": {"old-request", "old_request", "request", "target", "event"},
	"batch":                 {"file", "input", "path"},
}

// positionalNames says what that subject is called in the command's synopsis.
var positionalNames = map[string]string{
	"promise":               "<request>",
	"artifact":              "<path…>",
	"ratify":                "<target-event>",
	"supersede":             "<target-event>",
	"inspect":               "<event>",
	"provenance":            "<event>",
	"reassign-if-unclaimed": "<old-request-event>",
	"batch":                 "[file]",
}

// printExample writes the worked example under a command's flag list. A command
// with no example prints nothing rather than an invented one.
func printExample(set *flag.FlagSet) {
	example, known := commandExamples[set.Name()]
	if !known {
		return
	}
	fmt.Fprintf(set.Output(), "\nExample:\n  %s\n", example)
	fmt.Fprintf(set.Output(), "Reference: docs/reference/gs/%s.md\n", set.Name())
}

// usageErrorf refuses a malformed invocation. The command's own flags and one
// worked example go to standard error, and the reason comes back as the error
// the caller prints and exits non-zero on. Nothing is signed, nothing is read,
// and no key is touched: every call to this is before all of that.
func usageErrorf(set *flag.FlagSet, format string, arguments ...any) error {
	set.Usage()
	return fmt.Errorf(format, arguments...)
}

// parseRefusal answers an undefined flag whose name is the one a caller reaches
// for instead of this command's positional argument. The flag package has
// already printed its refusal and the usage this file adds the example to; this
// adds the line saying where the value belongs.
//
// It reads the parser's refusal rather than the raw arguments, because a scan of
// the arguments cannot tell a flag from a flag's value: it refused
// `gs supersede --text "--target" <event>`, a legitimate act whose reason begins
// with two dashes, while the joined spelling passed. The parser has consumed
// values and honoured the end-of-options marker already.
func parseRefusal(set *flag.FlagSet, err error) error {
	name, undefined := undefinedFlagName(err)
	if !undefined {
		return err
	}
	for _, guess := range positionalSubjects[set.Name()] {
		if name == guess {
			return fmt.Errorf("gs %s takes its %s as a positional argument, not --%s",
				set.Name(), positionalNames[set.Name()], name)
		}
	}
	return err
}

// undefinedFlagName reads the flag name out of the parser's own message for a
// flag it does not define. Anything else — a bad value, a help request — is not
// this case and travels on untouched.
func undefinedFlagName(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	name, found := strings.CutPrefix(err.Error(), "flag provided but not defined: -")
	if !found || name == "" || strings.ContainsAny(name, " =") {
		return "", false
	}
	return name, true
}

// usageReferenceError refuses an event reference that names nothing here. The
// resolver's own message says what went wrong with the reference; the usage and
// example beneath it say what the command expected to be given. The error
// itself is returned unwrapped, so a caller that classifies it still can.
func usageReferenceError(set *flag.FlagSet, err error) error {
	if err == nil {
		return nil
	}
	set.Usage()
	return err
}

// signingActorOrUsage resolves the identity this act will be signed with, and
// answers a missing one the way every malformed invocation is answered: the
// command's flags, one worked example, and the resolver's own message. A caller
// who did not say who they are has not typed a complete command.
func signingActorOrUsage(set *flag.FlagSet, flagValue string) (string, error) {
	actor, err := signingActor(flagValue)
	if err != nil {
		return "", usageReferenceError(set, err)
	}
	return actor, nil
}
