// The operator chooses the obligation. The service resolves the target head
// when filing and checks ancestry, roster and authority before signing.
export interface RequestResult {
  kind: "" | "none" | "inherit" | "target";
  ref: string;
  held: boolean;
  owner: string;
}
export const emptyRequestResult: RequestResult = { kind: "", ref: "", held: false, owner: "" };

export function requestResultBody(result: RequestResult, repo?: string): Record<string, string> | undefined {
  let body: Record<string, string>;
  switch (result.kind) {
    case "none": return { no_git_artifact: "true" };
    case "inherit": body = { target: "inherit" }; break;
    case "target":
      if (!repo || !result.ref.trim().startsWith("refs/heads/")) return undefined;
      body = { target_repo: repo, target_ref: result.ref.trim() };
      break;
    default: return undefined;
  }
  if (result.held) {
    if (!result.owner) return undefined;
    body.landing = "held";
    body.hold_owner = result.owner;
  }
  return body;
}

export function proseHoldWarning(text: string, result: RequestResult): boolean {
  return !(result.kind !== "none" && result.kind !== "" && result.held && result.owner) &&
    /\bhold\b|\bdo not (?:merge|land)\b|\bwait (?:to|before) (?:merge|merging|land|landing)\b/i.test(text);
}
