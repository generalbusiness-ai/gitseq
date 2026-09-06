import { useMemo } from "react";
import type { Projection } from "../lib/api";
import type { Workroom } from "../lib/store";
import { exactRecord, noteRows } from "../lib/notes";
import { eventTimestamp, firstLine, kindLabel } from "../lib/util";
import { RebuildNotice } from "./RebuildNotice";

export function ExactRecordResult({ projection, query, onOpen }: { projection?: Projection; query: string; onOpen: (event: string) => void }) {
  const record = exactRecord(projection, query);
  if (!record) return null;
  const statement = projection?.statements.find((item) => item.event === record.event);
  return <p className="my-2 text-sm"><button type="button" onClick={() => onOpen(record.event)} className="text-accent underline focus-visible:outline focus-visible:outline-accent">
    Open #{record.sequence} — {statement ? firstLine(statement.text, 100) : "durable record"}
  </button><span className="ml-2 text-xs text-muted">Exact record, across all views</span></p>;
}

export function Notes({ workroom, query, onQuery, onOpen }: { workroom: Workroom; query: string; onQuery: (query: string) => void; onOpen: (event: string) => void }) {
  const projection = workroom.status?.durable.projection;
  const vocabulary = workroom.status?.durable.vocabulary;
  const notes = useMemo(() => projection ? noteRows(projection, vocabulary, query) : [], [projection, vocabulary, query]);
  if (!projection) return <RebuildNotice />;
  return <section className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6" aria-label="Notes">
    <div className="mx-auto max-w-5xl">
      <label className="block text-xs text-muted" htmlFor="notes-search">Search notes or open an exact #number</label>
      <input id="notes-search" type="search" value={query} onChange={(event) => onQuery(event.target.value)}
        onKeyDown={(event) => { const record = exactRecord(projection, query); if (event.key === "Enter" && record) onOpen(record.event); }}
        className="mt-1 w-full max-w-md rounded border border-input bg-background px-3 py-2 text-sm focus-visible:outline focus-visible:outline-accent" />
      <ExactRecordResult projection={projection} query={query} onOpen={onOpen} />
      <h2 className="mt-4 font-serif text-lg">Notes <span className="font-sans text-sm text-muted">{notes.length}</span></h2>
      <p className="mb-3 text-xs text-muted">Notes under this room’s declared kind definitions. Requests and approval duties stay in Requests.</p>
      {notes.length === 0 && <p className="py-8 text-muted">{query ? "No notes match this search." : "No notes in this workroom yet."}</p>}
      <ul className="divide-y divide-border">{notes.slice(0, 200).map((note) => <li key={note.event} className="py-3">
        <button type="button" onClick={() => onOpen(note.event)} className="block w-full text-left focus-visible:outline focus-visible:outline-accent">
          <span className="text-sm text-foreground">{firstLine(note.text, 160)}</span>
          <span className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
            <span>#{note.sequence}</span><span>{projection.actors[note.actor]?.name ?? note.actor}</span>
            <span>{note.timestamp ? eventTimestamp(note.timestamp) : "date unavailable"}</span><span>{kindLabel(note.kind)}</span>
            {note.stale && <span>Reasoning moved</span>}{note.retired && <span>Retired</span>}
          </span>
        </button>
      </li>)}</ul>
      {notes.length > 200 && <p className="mt-4 text-xs text-muted">Showing the newest 200 of {notes.length} matching notes. Narrow the search to find earlier notes.</p>}
    </div>
  </section>;
}
