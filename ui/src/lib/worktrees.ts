import type { Commitment, GraphCommit, Projection, WorktreeView } from "./api";

export interface WorktreeAssociation {
  worktree: WorktreeView;
  expectedHead?: string;
  headMatches?: boolean;
}

export interface WorkGroups {
  available: Commitment[];
  inProgress: Commitment[];
  review: Commitment[];
}

export function groupOpenWork(commitments: Commitment[]): WorkGroups {
  return {
    available: commitments.filter((item) => item.status === "requested"),
    inProgress: commitments.filter((item) => item.status === "promised"),
    review: commitments.filter((item) => item.status === "reported"),
  };
}

// Associate local checkouts with a durable commitment using only explicit
// bridges: branch/head fields on the request loop, artifact provenance, or an
// ordinary implementation commit's Rests-On trailer. No checkout state is
// promoted back into the durable graph.
export function worktreesForCommitment(
  commitment: Commitment,
  projection: Projection,
  commits: GraphCommit[],
  worktrees: WorktreeView[],
): WorktreeAssociation[] {
  const anchors = new Set([commitment.request, commitment.promise, commitment.report].filter((id): id is string => Boolean(id)));
  const statements = new Map(projection.statements.map((statement) => [statement.event, statement]));
  const branchHints = new Set<string>();
  const explicitHeads = new Set<string>();

  for (const event of anchors) {
    const body = statements.get(event)?.body;
    if (body?.branch) branchHints.add(body.branch.replace(/^refs\/heads\//, ""));
    if (body?.head) explicitHeads.add(body.head);
    if (body?.commit) explicitHeads.add(body.commit);
  }

  const artifactHeads = new Set<string>();
  if (explicitHeads.size === 0) {
    for (const artifact of projection.artifacts) {
      if (!artifact.stale && dependsOnAny(artifact.event, anchors, projection.provenance)) artifactHeads.add(artifact.commit);
    }
  }
  const expectedHeads = explicitHeads.size > 0 ? explicitHeads : artifactHeads;

  const commitsByHead = new Map(commits.map((commit) => [commit.hash, commit]));
  return worktrees
    .filter((worktree) => {
      if (worktree.branch && branchHints.has(worktree.branch)) return true;
      if (worktree.head && expectedHeads.has(worktree.head)) return true;
      const commit = worktree.head ? commitsByHead.get(worktree.head) : undefined;
      return commit?.rests_on?.some((basis) => anchors.has(basis)) ?? false;
    })
    .map((worktree) => {
      const expectedHead = expectedHeads.size === 1 ? [...expectedHeads][0] : undefined;
      return {
        worktree,
        expectedHead,
        headMatches: expectedHead && worktree.head ? expectedHead === worktree.head : undefined,
      };
    });
}

function dependsOnAny(event: string, anchors: Set<string>, provenance: Record<string, string[]>): boolean {
  const seen = new Set<string>();
  const visit = (current: string): boolean => {
    if (anchors.has(current)) return true;
    if (seen.has(current)) return false;
    seen.add(current);
    return (provenance[current] ?? []).some(visit);
  };
  return visit(event);
}
