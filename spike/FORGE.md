# Optional forge ACL lane

The ordinary test suite uses a real smart-HTTP `git http-backend` behind a
repository-scoped authorization gate. It probes the important property directly:
knowing an object ID in repository B does not let credentials for repository A
fetch it.

`compose.yaml` adds a slower deployment check against Gitea. This is intentionally
optional: it pulls and boots third-party software, and forge behavior is a
deployment dependency rather than part of the gitseq kernel.

```sh
docker compose --profile forge up -d --wait
# Create two private repositories and repo-scoped users in the Gitea UI/API,
# then repeat the raw-OID fetch commands printed by the domain test.
docker compose --profile forge down
```

The spike does not claim per-ref read isolation. Its security domain is one
repository, so a real deployment must use separate private repositories where
read policy differs. The automated gate proves the protocol shape; this lane is
where a chosen forge's configuration and version are certified.
