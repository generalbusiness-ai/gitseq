import { BadgeCheck, FileWarning, Package } from "lucide-react";
import type { Projection } from "../lib/api";
import type { Selection } from "../lib/store";
import { cn, statusTint } from "../lib/util";

// The shelf: what this room has produced. Results first; the record is the
// explain-path, one click behind everything here.
export function Deliverables({
  projection,
  nameOf,
  onSelect,
}: {
  projection?: Projection;
  nameOf: (fp: string) => string;
  onSelect: (selection: Selection) => void;
}) {
  if (!projection) return <p className="p-4 text-sm text-faint">Loading…</p>;
  const decisionsInForce = projection.statements.filter((s) => s.kind === "propose" && s.ratified && !s.retired && !s.stale);
  const open = projection.commitments.filter((c) => ["requested", "promised", "reported"].includes(c.status));
  const done = projection.commitments.filter((c) => c.status === "satisfied");
  const requestText = (event: string) => projection.statements.find((s) => s.event === event)?.text ?? event;

  return (
    <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-4">
      <section>
        <SectionTitle icon={<Package className="h-3.5 w-3.5" />} title="Artifacts" />
        {projection.artifacts.length === 0 && <Empty>No artifacts yet — bridge real work with an artifact act.</Empty>}
        {projection.artifacts.map((artifact) => (
          <Row key={artifact.event} onClick={() => onSelect({ kind: "commit", id: artifact.commit })}>
            <span className={cn("w-14 shrink-0 text-[10px] font-semibold uppercase", artifact.stale ? "text-danger" : "text-ok")}>
              {artifact.stale ? "stale" : "current"}
            </span>
            <span className="truncate text-teal">
              {artifact.path}@{artifact.commit.slice(0, 8)}
            </span>
            {artifact.stale && <FileWarning className="ml-auto h-3.5 w-3.5 shrink-0 text-danger" />}
          </Row>
        ))}
      </section>
      <section>
        <SectionTitle icon={<BadgeCheck className="h-3.5 w-3.5" />} title="Decisions in force" />
        {decisionsInForce.length === 0 && <Empty>Nothing ratified and still standing.</Empty>}
        {decisionsInForce.map((decision) => (
          <Row key={decision.event} onClick={() => onSelect({ kind: "event", id: decision.event })}>
            <span className="truncate font-serif text-[13.5px]">{decision.text}</span>
          </Row>
        ))}
      </section>
      <section>
        <SectionTitle title={`Open commitments (${open.length})`} />
        {open.length === 0 && <Empty>Nobody owes anything.</Empty>}
        {open.map((commitment) => (
          <Row key={commitment.request + (commitment.promise ?? "")} onClick={() => onSelect({ kind: "event", id: commitment.request })}>
            <span className={cn("w-16 shrink-0 text-[11px] font-semibold", statusTint[commitment.status])}>{commitment.status}</span>
            <span className="truncate text-muted">{requestText(commitment.request)}</span>
            {commitment.waiting_on && <span className="ml-auto shrink-0 text-[10.5px] text-faint">⏳ {nameOf(commitment.waiting_on)}</span>}
          </Row>
        ))}
      </section>
      {done.length > 0 && (
        <section>
          <SectionTitle title={`Done (${done.length})`} />
          {done.map((commitment) => (
            <Row key={commitment.request} onClick={() => onSelect({ kind: "event", id: commitment.report ?? commitment.request })}>
              <BadgeCheck className="h-3.5 w-3.5 shrink-0 text-ok" />
              <span className="truncate text-muted">{requestText(commitment.request)}</span>
            </Row>
          ))}
        </section>
      )}
    </div>
  );
}

function SectionTitle({ icon, title }: { icon?: React.ReactNode; title: string }) {
  return (
    <h3 className="mb-1.5 flex items-center gap-1.5 text-[10.5px] font-medium uppercase tracking-[0.14em] text-faint">
      {icon}
      {title}
    </h3>
  );
}

function Row({ children, onClick }: { children: React.ReactNode; onClick: () => void }) {
  return (
    <button onClick={onClick} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-elevated/60 focus-visible:outline focus-visible:outline-accent">
      {children}
    </button>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="px-2 py-1 text-xs italic text-faint">{children}</p>;
}
