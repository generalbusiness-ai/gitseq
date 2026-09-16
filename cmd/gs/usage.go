package main

import (
	"flag"
	"fmt"
	"strings"
)

// commandNames is the list `gs` prints with no subcommand. It is also what
// every worked example below is checked against, so a command added here
// without an example is a test failure rather than a silent gap.
const commandNames = "init, actor-add, actor-retire, role-grant, role-revoke, actors, whoami, state, review, merge, merge-plan, ratify, supersede, reassign-if-unclaimed, batch, publish, status, work, artifacts, supersession-plan, staleness-wave, inspect, reviews, provenance, verify, checkpoint-clear, serve, attach"

// commandExamples is one worked invocation per command: the shortest line that
// actually works, in the same shape as that command's reference page under
// docs/reference/gs/. A person who mistyped a command needs to see one correct
// call more than they need a second copy of the flag list they just failed to
// use. Short references stand in for event ids, because the ids themselves are
// too long to read in an error and teach nothing.
var commandExamples = map[string]string{
	"init":                  `gs init --repo . --operator alice`,
	"actor-add":             `gs actor-add --as alice --name bot --kind agent`,
	"actor-retire":          "gs actor-retire --as alice --actor bot",
	"role-grant":            `gs role-grant --as alice --actor bot --role ratifier`,
	"role-revoke":           `gs role-revoke --as alice --actor bot --role ratifier`,
	"actors":                `gs actors`,
	"whoami":                `gs whoami --as alice`,
	"state":                 `gs state --as bot --kind promise --text 'I will do it' --rests-on '#42'`,
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
// at a command that wants `<target-event>` is the single commonest malformed
// invocation here, and the flag package's own answer — "flag provided but not
// defined" — never says where the value should have gone.
var positionalSubjects = map[string][]string{
	"ratify":                {"target", "event", "statement", "report"},
	"supersede":             {"target", "event", "statement"},
	"inspect":               {"event", "target"},
	"provenance":            {"event", "target"},
	"reassign-if-unclaimed": {"old-request", "old_request", "request", "target", "event"},
	"batch":                 {"file", "input", "path"},
}

// positionalNames says what that subject is called in the command's synopsis.
var positionalNames = map[string]string{
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

// refusePositionalAsFlag refuses a subject offered as a flag before the flag
// package can call it undefined, and says where the value belongs. It reads the
// raw arguments, because this has to happen before parsing: the flag package
// stops at the first thing it does not know, and by then its own message has
// already been printed.
func refusePositionalAsFlag(set *flag.FlagSet, arguments []string) error {
	guesses, watched := positionalSubjects[set.Name()]
	if !watched {
		return nil
	}
	for _, argument := range arguments {
		if argument == "--" {
			return nil
		}
		name, ok := flagName(argument)
		if !ok || set.Lookup(name) != nil {
			continue
		}
		for _, guess := range guesses {
			if name != guess {
				continue
			}
			return usageErrorf(set, "gs %s takes its %s as a positional argument, not --%s",
				set.Name(), positionalNames[set.Name()], name)
		}
	}
	return nil
}

// flagName reads the name out of one raw argument, in either of the two spellings
// the flag package accepts, and reports whether the argument was a flag at all.
func flagName(argument string) (string, bool) {
	if len(argument) < 2 || !strings.HasPrefix(argument, "-") {
		return "", false
	}
	name := strings.TrimPrefix(strings.TrimPrefix(argument, "-"), "-")
	if name == "" || strings.HasPrefix(name, "-") {
		return "", false
	}
	name, _, _ = strings.Cut(name, "=")
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
