# Security policy

## Release maturity and support boundary

Gitseq is a technical preview. It is suitable for local workrooms and offline
audit, but it is not yet a hardened multi-tenant service. The resident service
deliberately accepts loopback connections only. Do not expose it directly to a
network or use this preview as the only control protecting sensitive data.

Security fixes are made on the current `main` branch. There are no supported
release branches, backports, response-time commitments, or compatibility
guarantees yet. The six adversarial cases under `spike/` support only the
properties they name; they are not a general security certification.

## Public issues and private vulnerability reports

GitHub Issues are public. They are suitable for ordinary use and support
questions, feature requests, and non-sensitive bug reports. [Open an
issue][issues] for those topics. Do not put a vulnerability, exploit,
credential, private repository content, or personal data in an issue or in a
gitseq workroom.

Report vulnerabilities privately and directly to the maintainer. Do not open a
public issue for a security report.

Please include the affected commit, the impact you expect, steps or a small
reproducer, and any mitigations you already know. We will assess reports on a
best-effort basis and coordinate disclosure when a fix is ready. This preview
does not operate a bug bounty.

Repository visibility and issue availability are GitHub settings outside this
source tree. Source CI cannot guarantee those settings. Recheck them before
every public update.

[issues]: https://github.com/generalbusiness-ai/gitseq/issues
