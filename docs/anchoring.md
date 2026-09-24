---
title: Anchoring
summary: How each page names the acts that govern it, and the four gates that keep the set honest.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:e5868e1567e847bee1170b661ac8673f01ecb7a2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3e11a0d9e8061998f3e1b95f41242d7da5be20d2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
---

# Anchoring

Documentation that cannot go stale amounts to a rumour with good typography. Each
page in this set therefore names the durable acts that govern the
behaviour it describes, and ships with its own artifact statement resting
on those acts. When someone retires one of those acts, the projection marks
that page — and only the pages that named it — stale.

## Front matter

Every page opens with front matter:

```text
---
title: gs verify
summary: Check every signature and the integrity of the sequence.
rests_on:
  - git:sha1:<genesis>#git:sha1:<event>
---
```

`rests_on` names **implementation artifacts**, not the task that produced
the page. Apply this test: if someone retired this basis, would the
prose need re-checking? Retiring the request that asked for a page never
makes the page wrong, so a request does not serve as a basis. Retiring the artifact
for the code the page describes very much does.

Identifiers always take the full canonical form. An abbreviated citation
resolves to nothing, and an act that resolves to nothing can never flare.

## Page boundaries

The page forms the unit that goes stale, so page boundaries follow what would
falsify a page. Hence the `gs` reference gives one page per
subcommand: `gs verify` and `gs merge` change for unrelated reasons, and
a single combined page would flare for both, teaching readers to ignore
flares.

## The four gates

Four gates in `internal/docset` check the set; `make test` runs them,
and `make docs` runs them on their own.

| Gate | Test | What it catches |
|---|---|---|
| Surface completeness | `TestGateSurfaceCoversEveryCLISubcommand`, `TestGateSurfaceCoversEveryMCPTool` | A subcommand, flag, tool or argument added or removed without the reference page following. |
| Examples run | `TestGateDocumentedCommandsRun`, `TestGateEveryReferenceAndRecipePageRunsSomething` | A documented command that no longer works, or a page whose examples could never run. |
| No empty basis | `TestGateEveryPageNamesAGoverningAct`, `TestGateNoPageIsUnableToFlare`, `TestGateUnbridgedMarkStillFires`, `TestGateEveryNamedActResolvesToAUsableBasis` | A page with no anchor, a malformed identifier, an identifier that resolves to nothing, a basis other than an artifact, or the loss of the mark the convention depends on. |
| Flare | `TestGateRetiringOneActFlaresExactlyItsPages`, `TestGateVerifyPageCanFlareAlone` | Retiring one act flaring the wrong pages, in either direction. |

The surface gate reads the flags and tool schemas out of the
implementation source rather than from a list kept beside it, because the
same person who forgets the page forgets a hand-kept list. It
reads the whole `cmd/gs` package and `cmd/gitseq-mcp/main.go` — the
shipping commands — and fails loudly when it can no longer follow the
source, rather than reporting an empty surface that every page would
trivially match. The package rather than one file, because a subcommand
may live in a file of its own: a gate that looked only in `main.go`
would report such a command as taking no flags at all, and every page for
it would fail for a false reason.

The examples gate runs every block tagged `sh`. A block tagged `text` holds
a form, a file, or sample output, and the gate does not run it. So that the distinction
does not become an escape hatch, the gate also requires that every `gs`
subcommand page actually invokes its subcommand and that every recipe
runs something.

The flare gate cannot consult the workroom this repository lives in: a
page's own artifact gets filed after the commit containing the page, so a
test demanding to find it would turn red at exactly the commits it guards.
It replays the declared graph into a scratch workroom instead, giving
each named act a stand-in and each page an artifact resting on the right
stand-ins, and then reads the same marks the real projection shows.

A page's **bases** raise a different matter. A governing event exists
before the work that names it — the `Rests-On:` trailer means exactly
that — so one gate does resolve them against the real record, and fails
if a named act does not exist there or does not form an artifact. Without it, front
matter naming a well-formed identifier that stands for nothing would
model perfectly and anchor nothing. That gate skips when the checkout
holds no workroom to resolve against.

No gate here can settle whether a page resolves to the *right*
artifact. That calls for a judgement about what the prose claims
against what the code does, and it stays with the reviewer.

## Recording a page's artifact

After the commit that adds or changes a page, point at it:

```text
gs state --repo . --as <you> --kind artifact \
  --text 'docs/reference/gs/verify.md at <head>' \
  --body path=docs/reference/gs/verify.md --body commit=<full-commit-id> \
  --rests-on '<governing-act>'
```

When a later change to the same page lands, that new artifact statement
supersedes the previous artifact for the same path. Recording the
succession lets a reader tell a current page from a forgotten
one; `gs status` marks an artifact whose predecessor at the same path
remains live as **succession not recorded**.

## When someone retires a basis

Naming the successor lets the pages survive the
retirement. A supersession that rests on an artifact standing at the same path,
or at a directory covering it, tells every page naming the old pointer
where the behaviour went; the basis gate reports that as a flare and the
page re-anchors when someone re-reads the prose against the code. A
supersession that names no such artifact leaves the page pointing at a
hole, and the gate fails.

Hence [`gs merge`](reference/gs/merge.md) may retire a pointer the
set still cites. It publishes the successor and names it in the same
batch, so the link exists before anyone reads it. It refuses when the
change deletes a path outright, because then no successor exists to
name.

## Known limit

Staleness does not propagate through bases the fold judged ineffective.
A page anchored to an act that lands ineffective goes quiet rather than
stale. See [Staleness](concepts/staleness.md).
