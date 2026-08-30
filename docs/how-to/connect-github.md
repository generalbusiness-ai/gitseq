---
title: Connect GitHub
summary: Bring selected GitHub issues into a workroom, turn them into assigned work, and publish an exact candidate as a pull request.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:66e0e12172925f497f0dde1b910e705b157c08e7
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8a446fa7fc174547b967d1676ed0844d569b5eb0
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9b714309ab6aa17154b96083c9d7fc054a9218d
---

# Connect GitHub

Use `gitseq-github` to bring selected GitHub issues into a workroom and to
open a pull request for an exact implementation candidate. GitHub remains the
place where people discover and discuss the issue. Gitseq records who chose to
act on it, who accepted the work, and which commit was reviewed.

> **Status:** the connector runs one issue-observation pass at a time. It can
> open a pull request, but it does not watch continuously, import comments or
> reviews, or synchronize later issue changes.

## What you need

- an initialized gitseq workroom and the name of one of its operators;
- `gs` and `gitseq-github` on your `PATH`;
- the GitHub owner and repository name; and
- a `GITHUB_TOKEN` that can read the selected issues and, if you will open pull
  requests, create pull requests in that repository.

Public issue reads can work without a token. Keep any token out of command
history, configuration files, durable events, and documentation. Give it only
the GitHub permissions the connector needs.

The runnable setup below uses a disposable workroom. For a real connection,
use your existing repository and operator instead of the first four commands.

## 1. Add the GitHub connector to the workroom

Create one service actor for the connector. It signs the issue observations
that enter the workroom.

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
INIT=$(gs init --repo "$REPO" --operator alice)

OPERATOR=alice
CONNECTOR=github-connector
OWNER=example
GITHUB_REPOSITORY=project

SEED=$(printf '%s\n' "$INIT" | sed -n 's/.*"seed": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as "$OPERATOR" \
  --name "$CONNECTOR" --kind service >/dev/null
```

Outcome: `gs actors --repo "$REPO"` lists `github-connector` as a service
actor. Its private key stays in the workroom's local Gitseq configuration.

## 2. Record what the connector may do

Create and ratify a charter for this GitHub repository. The example permits
both supported operations: observing issues and opening pull requests. Use
`operations=observe` if this connection should be read-only.

```sh
CHARTER=$(gs state --repo "$REPO" --as "$OPERATOR" --kind propose \
  --text "Allow $CONNECTOR to exchange work with $OWNER/$GITHUB_REPOSITORY" \
  --body connector=github \
  --body owner="$OWNER" \
  --body repo="$GITHUB_REPOSITORY" \
  --body actor="$CONNECTOR" \
  --body operations='observe propose' \
  --rests-on "$SEED")

gs ratify --repo "$REPO" --as "$OPERATOR" "$CHARTER" >/dev/null
```

Outcome: the connector can act only when the command names this live charter
and the same GitHub owner, repository, actor, and operation.

## 3. Choose the issues to bring in

An operator chooses the scope by stating an admission clause under the
charter. To choose particular issues:

```sh
CLAUSE=$(gs state --repo "$REPO" --as "$OPERATOR" --kind assert \
  --text "Bring GitHub issues 42 and 57 into the workroom" \
  --body connector=github --body issues='42,57' \
  --rests-on "$CHARTER")
```

You can instead choose issues by state and labels:

```text
CLAUSE=$(gs state --repo "$REPO" --as "$OPERATOR" --kind assert \
  --text 'Bring open GitHub issues labelled ready into the workroom' \
  --body connector=github --body state=open --body labels=ready \
  --rests-on "$CHARTER")
```

When valid issue numbers are present, they select those issues. Otherwise,
`state` and `labels` form a criteria clause, and all criteria it states must
match. `state` may be `open`, `closed`, or `all`; `issues` and `labels` accept
comma-separated values. A clause that names no valid issue, state, or label
selects nothing.

Outcome: the workroom has a signed, reviewable statement of which GitHub
issues may enter. Retire or replace the clause when that scope should stop or
change.

## 4. Preview and import the selected issues

Run a preview first. Substitute your real token and repository values if you
used the disposable setup above.

```text
GITHUB_TOKEN="$GITHUB_TOKEN" GITSEQ_ACTOR="$CONNECTOR" gitseq-github \
  --repo "$REPO" \
  --charter "$CHARTER" \
  --owner "$OWNER" --repo-name "$GITHUB_REPOSITORY" \
  --dry-run
```

The preview prints each issue it would observe and makes no durable change.
Remove `--dry-run` when the selection is right:

```text
GITHUB_TOKEN="$GITHUB_TOKEN" GITSEQ_ACTOR="$CONNECTOR" gitseq-github \
  --repo "$REPO" \
  --charter "$CHARTER" \
  --owner "$OWNER" --repo-name "$GITHUB_REPOSITORY"
```

Outcome: each new issue is recorded once as an `assert`, signed by the
connector and resting on the charter and the clause that selected it. The
record includes the issue number, title, URL, and GitHub author. It does not
copy the issue body.

Running the command again does not duplicate an issue already observed by
this connector. It also does not record later edits, label changes, closure,
or reopening.

If several processes may append concurrently, send observations through the
resident by adding its explicit loopback URL:

```text
  --server http://127.0.0.1:7777
```

## 5. Turn an observation into work

An observation reports what exists on GitHub; it does not assign anyone to do
anything. When a workroom member decides to act, they create a request resting
on the observation printed by the import:

```text
REQUEST=$(gs state --repo "$REPO" --as "$OPERATOR" --kind request \
  --text 'Resolve GitHub issue 42' \
  --body to=@worker \
  --body conditions='the issue is fixed, tested, and reviewed at an exact head' \
  --rests-on 'git:sha1:<genesis>#git:sha1:<observation>')
```

Outcome: the issue is now ordinary workroom work. The addressee can promise
it, implement it on a branch, publish an exact-head artifact, and request
independent review. GitHub text remains quoted foreign input; the signed
request is the decision to act.

Follow [Run a work loop](run-a-work-loop.md) for the implementation and review
steps.

## 6. Publish the candidate as a pull request

Push the candidate branch to GitHub, then collect these exact values:

- the positive GitHub issue number;
- the pushed branch and target branch;
- the full commit ID at the candidate head;
- the effective request event; and
- the live artifact event naming that same commit.

The current command requires the artifact to cite the request directly. An
artifact that cites a promise instead is valid workroom history, but this
connector version cannot publish it. Do not reshape the record merely to make
the command pass; open that pull request outside the connector until
commitment-chain support is available.

Check the branch and commit yourself, because the connector does not compare
them:

```text
test "$(git -C "$REPO" rev-parse "$BRANCH")" = "$HEAD_COMMIT"
git -C "$REPO" push origin "$BRANCH"
```

Preview the pull request:

```text
GITHUB_TOKEN="$GITHUB_TOKEN" GITSEQ_ACTOR="$CONNECTOR" gitseq-github \
  --repo "$REPO" \
  --charter "$CHARTER" \
  --owner "$OWNER" --repo-name "$GITHUB_REPOSITORY" \
  --propose 42 \
  --branch "$BRANCH" --base main \
  --commit "$HEAD_COMMIT" \
  --request "$REQUEST" --artifact "$ARTIFACT" \
  --title 'Resolve issue 42' \
  --dry-run
```

Read the rendered body, then remove `--dry-run` to create the pull request.
Run the write once: repeating it can open a duplicate pull request.

Outcome: GitHub receives a pull request that points to the workroom request,
artifact, and exact candidate head. Review, approval, and merge still happen
through the Gitseq work loop. Record the printed pull-request number and URL
as evidence when you report delivery.

## Current limits

The connector does not currently:

- watch GitHub or install a webhook;
- import issue comments, pull requests, reviews, or merges;
- turn an observation into a request automatically;
- update an observation after the first import;
- post or maintain status comments; or
- store or refresh GitHub credentials.

## See also

- [Run a work loop](run-a-work-loop.md) — claim, implement, review, and merge
  the work after an issue is selected.
- [Deploy a resident](deploy-a-resident.md) — sequence concurrent appends
  through the local service.
- [Connectors](../concepts/connectors.md) — the boundary between foreign
  content and signed workroom authority.
