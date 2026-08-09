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

## Report a vulnerability privately

Use [GitHub private vulnerability reporting][report] for vulnerabilities. Do
not put a vulnerability, exploit, credential, private repository content, or
personal data in a public issue or in a gitseq workroom.

Please include the affected commit, the impact you expect, steps or a small
reproducer, and any mitigations you already know. We will assess reports on a
best-effort basis and coordinate disclosure when a fix is ready. This preview
does not operate a bug bounty.

This technical preview does not yet operate a public support tracker. Security
reports belong in the private channel above.

[report]: https://github.com/generalbusiness-ai/gitseq/security/advisories/new
