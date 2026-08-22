# Optional forge ACL lane

The ordinary test suite uses a real smart-HTTP `git http-backend` behind a
repository-scoped authorization gate. It probes the important property directly:
knowing an object ID in repository B does not let credentials for repository A
fetch it.

`compose.yaml` adds a slower deployment check against Gitea. This is intentionally
optional: it pulls and boots third-party software, and forge behavior is a
deployment dependency rather than part of the gitseq kernel.

## Trust boundary

This forge is reachable only from the host that runs it. It publishes on
`127.0.0.1` and first-run setup is locked, because a development Gitea has no
authentication story of its own worth exposing: on a fresh data volume an
unlocked installer hands administrator to whoever loads the page first, and a
bare `3300:3000` mapping offers that page to every network the host is attached
to. Neither guard is a preference. `compose.yaml` carries both and
`spike/forge` fails the build if either is dropped.

Locking setup removes the browser path to the first administrator, so make it
through the CLI instead.

```sh
docker compose --profile forge up -d --wait

# Verify the boundary before trusting the lane. The first check fails visibly
# if the port is published anywhere but loopback. The second observes the real
# state transition: v1.24.6 serves the installer at `/` until setup is locked,
# so a login page at /user/login is what distinguishes a locked server from one
# still offering installation. Probing /install proves nothing either way -- it
# is not-found in both states.
docker compose --profile forge port forge 3000 | grep -q '^127\.0\.0\.1:' \
  || { echo "REFUSING: forge is not published on loopback"; exit 1; }
curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3300/user/login \
  | grep -q '^200$' \
  || { echo "REFUSING: forge did not serve the login page; setup may be open"; exit 1; }

# The locked installer means no administrator exists yet. Create one.
docker compose --profile forge exec -u git forge \
  gitea admin user create --admin --username spike-admin \
  --email spike-admin@example.invalid --random-password

# Then create two private repositories and repo-scoped users through the UI at
# http://127.0.0.1:3300 or the API, and repeat the raw-OID fetch commands
# printed by the domain test.
docker compose --profile forge down
```

The spike does not claim per-ref read isolation. Its security domain is one
repository, so a real deployment must use separate private repositories where
read policy differs. The automated gate proves the protocol shape; this lane is
where a chosen forge's configuration and version are certified.
