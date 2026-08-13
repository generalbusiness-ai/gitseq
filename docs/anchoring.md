---
title: Anchoring
summary: How each page names the acts that govern it, and the four gates that keep the set honest.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8fed27446d2441119f676fd3fb0b3cbb3a038ec8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcdbd7cb5b058a09c59b4540d5157f46116026bc
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b749bdeade8afec544344727fb77b5c89348e705
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6adcbc51bb03c6c0b0f8ba6601f23194b033f4b9
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f79617aa07d856b3773f60aa2ae464d41c34fbe5
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# Anchoring

Documentation that cannot go stale is a rumour with good typography. Each
page in this set therefore names the durable acts that govern the
behaviour it describes, and ships with its own artifact statement resting
on those acts. When one of those acts is retired, that page — and only
the pages that named it — is marked stale.

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
the page. The test to apply is: if this basis were retired, would the
prose need re-checking? Retiring the request that asked for a page never
makes the page wrong, so a request is not a basis. Retiring the artifact
for the code the page describes very much does.

Identifiers are always the full canonical form. An abbreviated citation
resolves to nothing, and an act that resolves to nothing can never flare.

## Page boundaries

A page is the unit that goes stale, so page boundaries follow what would
falsify a page. That is why the `gs` reference is one page per
subcommand: `gs verify` and `gs merge` change for unrelated reasons, and
a single combined page would flare for both, teaching readers to ignore
flares.

## The four gates

The set is checked by four gates in `internal/docset`, run by `make test`
and on their own by `make docs`.

| Gate | Test | What it catches |
|---|---|---|
| Surface completeness | `TestGateSurfaceCoversEveryCLISubcommand`, `TestGateSurfaceCoversEveryMCPTool` | A subcommand, flag, tool or argument added or removed without the reference page following. |
| Examples run | `TestGateDocumentedCommandsRun`, `TestGateEveryReferenceAndRecipePageRunsSomething` | A documented command that no longer works, or a page whose examples were never executable. |
| No empty basis | `TestGateEveryPageNamesAGoverningAct`, `TestGateNoPageIsUnableToFlare`, `TestGateUnbridgedMarkStillFires`, `TestGateEveryNamedActResolvesToALiveRecord` | A page with no anchor, a malformed identifier, an identifier that resolves to nothing, a basis that is not an artifact, or the loss of the mark the convention depends on. |
| Flare | `TestGateRetiringOneActFlaresExactlyItsPages`, `TestGateVerifyPageCanFlareAlone` | Retiring one act flaring the wrong pages, in either direction. |

The surface gate reads the flags and tool schemas out of the
implementation source rather than from a list kept beside it, because a
hand-kept list is forgotten by the same person who forgets the page. It
reads `cmd/gs/main.go` and `cmd/gitseq-mcp/main.go` — the shipping
commands — and fails loudly when it can no longer follow the source,
rather than reporting an empty surface that every page would trivially
match.

The examples gate runs every block tagged `sh`. A block tagged `text` is
a form, a file, or sample output, and is not run. So that the distinction
does not become an escape hatch, the gate also requires that every `gs`
subcommand page actually invokes its subcommand and that every recipe
runs something.

The flare gate cannot consult the workroom this repository lives in: a
page's own artifact is filed after the commit containing the page, so a
test demanding to find it would be red at exactly the commits it guards.
It replays the declared graph into a scratch workroom instead, giving
each named act a stand-in and each page an artifact resting on the right
stand-ins, and then reads the same marks the real projection shows.

A page's **bases** are a different matter. A governing event exists
before the work that names it — that is what the `Rests-On:` trailer
means — so one gate does resolve them against the real record, and fails
if a named act is not there or is not an artifact. Without it, front
matter naming a well-formed identifier that stands for nothing would
model perfectly and anchor nothing. That gate skips when the checkout
holds no workroom to resolve against.

What no gate here can settle is whether the artifact a page resolves to
is the *right* one. That is a judgement about what the prose claims
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
succession is what lets a reader tell a current page from a forgotten
one; `gs status` marks an artifact whose predecessor at the same path is
still live as **succession not recorded**.

## When a basis is retired

Naming the successor is what makes the retirement survivable for the
pages. A supersession that rests on an artifact standing at the same path,
or at a directory covering it, tells every page naming the old pointer
where the behaviour went; the basis gate reports that as a flare and the
page re-anchors when someone re-reads the prose against the code. A
supersession that names no such artifact leaves the page pointing at a
hole, and the gate fails.

This is why [`gs merge`](reference/gs/merge.md) may retire a pointer the
set still cites. It publishes the successor and names it in the same
batch, so the link exists before anyone reads it. It refuses when the
change deletes a path outright, because then there is no successor to
name.

## Known limit

Staleness does not propagate through bases that were judged ineffective.
A page anchored to an act that lands ineffective goes quiet rather than
stale. See [Staleness](concepts/staleness.md).
