import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useWorkroom, provenanceClosure, type Selection } from "./lib/store";
import { useSession } from "./lib/session";
import { TopBar } from "./components/TopBar";
import { Stream, type PendingSay } from "./components/Stream";
import { Composer, type ComposerContext } from "./components/Composer";
import { WorkDrawer } from "./components/WorkDrawer";

const emptyComposer: ComposerContext = { setDown: false, restsOn: [], frames: [] };

// The Room is the only permanent center; Work, the repository, and the
// durable record live behind the header chip, in an overlay drawer.
export default function App() {
  const workroom = useWorkroom();
  const session = useSession();
  const [selection, setSelection] = useState<Selection>();
  const [composer, setComposer] = useState<ComposerContext>(emptyComposer);
  const [drawerOpen, setDrawerOpen] = useState(false);
  // Optimistic say echoes: appended on send, reconciled when the frame lands.
  const [pending, setPending] = useState<PendingSay[]>([]);

  const projection = workroom.status?.durable.projection;
  const statements = projection?.statements ?? [];
  const highlight = useMemo(
    () => provenanceClosure(selection, projection?.provenance ?? {}, workroom.commits, statements),
    [selection, projection, workroom.commits, statements],
  );

  // Clicking a thing toggles its selection; basis links always select.
  const select = (next: Selection) =>
    setSelection((current) =>
      current && current.kind === next.kind && current.id === next.id ? undefined : next,
    );
  const jump = useCallback((next: Selection) => setSelection(next), []);

  const echoSay = useCallback((text: string) => {
    const id = crypto.randomUUID();
    setPending((list) => [...list, { id, text, at: Date.now() }]);
    return id;
  }, []);
  const dropPending = useCallback(
    (ids: string[] | string) => {
      const gone = new Set(Array.isArray(ids) ? ids : [ids]);
      setPending((list) => list.filter((p) => !gone.has(p.id)));
    },
    [],
  );

  return (
    <div className="flex h-full flex-col">
      <TopBar workroom={workroom} session={session} onOpenWork={() => setDrawerOpen(true)} />
      <main className="flex min-h-0 flex-1 flex-col">
        <Stream
          workroom={workroom}
          session={session}
          highlight={highlight}
          selection={selection}
          onSelect={select}
          onJump={jump}
          composer={composer}
          onComposer={setComposer}
          pending={pending}
          onReconcile={dropPending}
        />
        <Composer
          workroom={workroom}
          session={session}
          context={composer}
          onContext={setComposer}
          onSay={echoSay}
          onSayFailed={dropPending}
        />
      </main>
      {drawerOpen && (
        <WorkDrawer
          workroom={workroom}
          highlight={highlight}
          selection={selection}
          onSelect={select}
          onJump={jump}
          onClose={() => setDrawerOpen(false)}
        />
      )}
      {!session.actor && <JoinGate workroom={workroom} onJoin={session.setActor} />}
    </div>
  );
}

// Joining is a custody decision, made explicitly — never defaulted.
function JoinGate({ workroom, onJoin }: { workroom: ReturnType<typeof useWorkroom>; onJoin: (name: string) => void }) {
  const first = useRef<HTMLButtonElement>(null);
  // Focus lands on the first identity as soon as the roster arrives.
  useEffect(() => {
    first.current?.focus();
  }, [workroom.actors.length > 0]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 p-4 backdrop-blur-sm">
      <div role="dialog" aria-modal="true" aria-labelledby="join-title" className="w-96 max-w-full rounded-xl border border-border bg-card p-6 shadow-2xl">
        <h2 id="join-title" className="font-serif text-lg font-semibold">
          Join the workroom
        </h2>
        <p className="mt-1 text-xs leading-relaxed text-muted">
          Everything you set down is signed as the identity you choose, permanently and publicly.
          This service holds the keys; choose who you are.
        </p>
        <div className="mt-4 space-y-1.5">
          {workroom.actors.length === 0 && <p className="text-xs italic text-faint">Waiting for the room…</p>}
          {workroom.actors.map((actor, index) => (
            <button
              key={actor.name}
              ref={index === 0 ? first : undefined}
              onClick={() => onJoin(actor.name)}
              className="flex w-full items-center justify-between rounded-lg border border-border px-3 py-2 text-left text-sm hover:border-accent/50 hover:bg-elevated focus-visible:outline focus-visible:outline-accent"
            >
              <span className="font-medium">{actor.name}</span>
              <span className="text-xs text-faint">{actor.role}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
