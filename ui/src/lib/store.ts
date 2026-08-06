import { useEffect, useRef, useState } from "react";
import { api, type Actor, type Cursor, type GraphCommit, type Statement, type Status } from "./api";

export interface Workroom {
  status?: Status;
  commits: GraphCommit[];
  actors: Actor[];
  offline: boolean;
}

// One wait-loop drives the whole page: the composite cursor is the only
// client state the server ever needs back, per the stateless contract.
export function useWorkroom(): Workroom {
  const [status, setStatus] = useState<Status>();
  const [commits, setCommits] = useState<GraphCommit[]>([]);
  const [actors, setActors] = useState<Actor[]>([]);
  const [offline, setOffline] = useState(false);
  const headRef = useRef<string>("");

  useEffect(() => {
    let stopped = false;
    let cursor: Cursor | undefined;

    const refreshGraph = async (head: string) => {
      if (head === headRef.current) return;
      headRef.current = head;
      try {
        const graph = await api.graph();
        if (!stopped) setCommits(graph.commits);
      } catch {
        /* graph is decoration; the durable pane still renders */
      }
    };

    const apply = (next: Status) => {
      if (stopped) return;
      setStatus(next);
      setOffline(false);
      cursor = next.cursor;
      void refreshGraph(next.durable.head);
    };

    const loop = async () => {
      api.actors().then((list) => !stopped && setActors(list)).catch(() => {});
      while (!stopped) {
        try {
          if (!cursor) {
            apply(await api.status());
          } else {
            const wait = await api.wait(cursor);
            apply(wait.status);
          }
        } catch {
          if (!stopped) setOffline(true);
          await new Promise((resolve) => setTimeout(resolve, 2000));
        }
      }
    };
    void loop();
    return () => {
      stopped = true;
    };
  }, []);

  return { status, commits, actors, offline };
}

export interface Selection {
  kind: "event" | "commit";
  id: string;
}

// The provenance closure of a selection, for cross-pane highlighting.
export function provenanceClosure(
  selection: Selection | undefined,
  provenance: Record<string, string[]>,
  commits: GraphCommit[],
  statements: Statement[] = [],
): { events: Set<string>; commits: Set<string> } {
  const events = new Set<string>();
  const commitSet = new Set<string>();
  if (!selection) return { events, commits: commitSet };

  const walk = (id: string) => {
    if (events.has(id)) return;
    events.add(id);
    for (const basis of provenance[id] ?? []) walk(basis);
  };

  if (selection.kind === "event") {
    walk(selection.id);
  } else {
    commitSet.add(selection.id);
    const commit = commits.find((c) => c.hash === selection.id);
    for (const cited of commit?.rests_on ?? []) walk(cited);
    // Artifact statements citing this commit join the association.
    for (const statement of statements) {
      if (statement.kind === "artifact" && statement.body?.commit === selection.id) walk(statement.event);
    }
  }
  // Any commit citing a highlighted event joins the highlight.
  for (const commit of commits) {
    if (commit.rests_on?.some((cited) => events.has(cited))) commitSet.add(commit.hash);
  }
  return { events, commits: commitSet };
}
