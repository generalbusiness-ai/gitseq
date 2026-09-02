import type { LocalRepo } from "./api";

// The tab names the project the service serves: the last segment of the
// served checkout's path. The worktree list cannot improve on that, because
// the service sorts the current checkout first and marks no entry as the
// main working tree, so a service started from a linked worktree is named
// after that worktree. A bare ".git" suffix is not a name.
export function projectName(local: Pick<LocalRepo, "repo"> | undefined): string | undefined {
  const segments = (local?.repo ?? "").split(/[\\/]+/).filter((segment) => segment.length > 0);
  let name = segments[segments.length - 1] ?? "";
  if (name.endsWith(".git")) name = name.slice(0, -".git".length);
  return name || undefined;
}

// "workroom" alone is the truthful title before the service has answered or
// when it cannot say what it serves.
export function tabTitle(project: string | undefined): string {
  return project ? `${project} workroom` : "workroom";
}
