---
title: Event identifiers
summary: The one canonical name every durable event is cited by.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbb34351b80ceeb7575e112ef7324b3d5de569ac
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
---

# Event identifiers

Every durable event has exactly one canonical identifier:

```text
git:<object-format>:<genesis>#git:<object-format>:<event-commit>
```

For example:

```text
git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cc7eb5a129850990652c553adffbfd2f4f726f83
```

The part before `#` names the **workroom**, by the genesis hash. The part
after names the **event**, by its commit in `refs/seq/<genesis>`. Both
carry the object format, so a sha256 repository is unambiguous.

## Where it is used

- `--rests-on` on `gs state` and `gs supersede`
- the positional target of `gs ratify` and `gs supersede`
- the argument to `gs provenance`
- `--artifact` and `--promise` on `gs review`, `--approval` on `gs merge`
- `rests_on`, `target` and `about` in the MCP tools
- `Rests-On:` trailers on ordinary implementing commits

## Copy it whole

Always copy the identifier from the emitted event, not from a display
that abbreviates it. `gs status` shortens identifiers for reading —
`git:sha1…02aa808` — and those short forms are not identifiers.

An identifier for this workroom that names no event in it is **refused**
before it is sequenced, whatever the kind. The identifier asserts that
the event is at a position in this log, and that is the one claim the
substrate can settle without knowing what any kind means, so a mistyped
or invented one comes back as a refusal naming the reference rather than
as a record nobody can repair. The gate is on new records only:
identifiers that dangle in existing history stay in it, and still read.

A citation the substrate cannot resolve at all is still **accepted**.
Another workroom's identifier, or a string that is not an identifier,
asserts nothing about this log. On `assert`, `artifact` and `propose`
there is no required edge either, so such a basis is simply kept and the
act records as effective. What you lose is silent: the fold marks an
artifact whose bases all fail to resolve as `unable to flare`, because
`supersede` needs a target it can resolve and so nothing could ever make
that act stale.

Events with a required edge are stricter, and `ratify` is strictest of
all — it refuses any citation other than its target. See
[The record](../concepts/record.md#recorded-is-not-effective).

## Getting one

Every durable `gs` command that appends prints the identifier of what it
appended, and nothing else, so it can be captured directly:

```text
EVENT=$(gs state --repo . --as alice --kind assert --text 'a claim' --rests-on "$SEED")
```

`gs init` prints the genesis hash and the seed event. The seed is the one
act that rests on nothing and is the usual first basis in a new
workroom:

```text
git:sha1:<genesis>#git:sha1:$(git -C <repo> rev-parse refs/seq/<genesis>)
```

`gs status --json` includes the full identifier of every event.

## Related identifiers that are not event identifiers

| Looks like | Is |
|---|---|
| A 40- or 64-character hex string | An ordinary git commit. Artifacts cite these in `body.commit`. |
| A 64-character hex string in `actor` | An actor fingerprint. |
| `session:` followed by hex | An opaque presence handle. It grants nothing and is not durable. |
| A pull request URL | A hint. Never rest a durable act on a bare URL; cite the head commit. |
