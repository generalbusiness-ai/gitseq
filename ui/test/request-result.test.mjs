import test from "node:test";
import assert from "node:assert/strict";
import { emptyRequestResult, proseHoldWarning, requestResultBody } from "../src/lib/requestResult.ts";
import { RetryKeys } from "../src/lib/interaction.ts";

test("a request requires an explicit result and service-resolved target head", () => {
  assert.equal(requestResultBody(emptyRequestResult, "repo"), undefined);
  assert.equal(requestResultBody({ ...emptyRequestResult, kind: "target" }, "repo"), undefined);
  assert.equal(requestResultBody({ ...emptyRequestResult, kind: "target", ref: "main" }, "repo"), undefined);
  assert.equal(requestResultBody({ ...emptyRequestResult, kind: "target", ref: "refs/heads/release-2" }), undefined);
  const body = requestResultBody({ ...emptyRequestResult, kind: "target", ref: "refs/heads/release-2" }, "repo");
  assert.deepEqual(body, { target_repo: "repo", target_ref: "refs/heads/release-2" });
  assert.equal("target_head" in body, false, "browser must not invent a filing measurement");
  assert.deepEqual(requestResultBody({ ...emptyRequestResult, kind: "inherit" }), { target: "inherit" });
  assert.deepEqual(requestResultBody({ ...emptyRequestResult, kind: "none", held: true, owner: "old" }), { no_git_artifact: "true" });
});

test("a hold requires its owner and prose does not supply authority", () => {
  const held = { ...emptyRequestResult, kind: "inherit", held: true };
  assert.equal(requestResultBody(held), undefined);
  assert.equal(proseHoldWarning("Do not merge until next week", emptyRequestResult), true);
  assert.equal(proseHoldWarning("hold this", held), true);
  assert.equal(proseHoldWarning("hold this", { ...held, owner: "owner" }), false);
  assert.deepEqual(requestResultBody({ ...held, owner: "owner" }), { target: "inherit", landing: "held", hold_owner: "owner" });
});

test("unchanged result retries retain one intent and edited destinations do not", () => {
  let counter = 0;
  const keys = new RetryKeys(() => `key-${++counter}`);
  const payload = (ref) => JSON.stringify(requestResultBody({ ...emptyRequestResult, kind: "target", ref }, "repo"));
  const key = keys.forAttempt("request", payload("refs/heads/release-2"));
  assert.equal(keys.forAttempt("request", payload("refs/heads/release-2")), key);
  assert.notEqual(keys.forAttempt("request", payload("refs/heads/main")), key);
});
