---
title: Getting started
summary: Clone, build, and take the first path into the documentation set.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1a33b0645b9bd51851cdd9d1787c63a94b993d6a
---

# Getting started

Requires **Go 1.26** and **Git with SSH signing support**.

```text
git clone https://github.com/generalbusiness-ai/gitseq.git
cd gitseq
make test
make vet
make build
```

`make build` puts `gs` and `gitseq-mcp` in `bin/`.

From here, [Do a piece of work, end to end](how-to/end-to-end.md) walks
one complete path — an empty directory to an audited record in a fresh
clone — and the [documentation map](README.md) lays out every other
route: how-to guides for a task you already have, concepts for why the
system behaves as it does, and a reference page for every command and
tool.
