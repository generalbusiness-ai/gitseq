import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useWorkroom } from "./lib/store";
import { buildRecordIndex } from "./lib/records";
import { useSession } from "./lib/session";
import { useFrames } from "./lib/frames";
import { api, type ActInput } from "./lib/api";
import { TopBar } from "./components/TopBar";
import { RequestList, defaultListView, type ListView } from "./components/RequestList";
import { Thread, type PendingSay } from "./components/Thread";
import { Avatar } from "./components/Avatar";
import { reconciledPendingIDs, RetryKeys } from "./lib/interaction";

// Two screens. The list is the default and answers the whole question; the
// thread answers "what does this one wait on?". There is no third
// destination, and no presentation to choose between.
type Screen = { kind: "list" } | { kind: "thread"; event: string };

export default function App() {
  const workroom = useWorkroom();
  const session = useSession();
  const { frames } = useFrames(workroom);
  const [screen, setScreen] = useState<Screen>({ kind: "list" });
  const [listView, setListView] = useState<ListView>(defaultListView);
  // Optimistic say echoes: appended on send, reconciled when the frame lands.
  const [pending, setPending] = useState<PendingSay[]>([]);

  const projection = workroom.status?.durable.projection;

  // Any record opens the thread it belongs to, resolved one way for every
  // caller: a promise, report, act, artifact or assert lands on its request.
  const index = useMemo(() => (projection ? buildRecordIndex(projection) : undefined), [projection]);
  const openThread = useCallback(
    (event: string) => setScreen({ kind: "thread", event: index ? index.threadRoot(event) : event }),
    [index],
  );
  const showList = useCallback(() => setScreen({ kind: "list" }), []);

  const echoSay = useCallback((text: string, re?: string, about?: string) => {
    const id = crypto.randomUUID();
    setPending((list) => [...list, { id, text, at: Date.now(), re, about }]);
    return id;
  }, []);
  const dropPending = useCallback((ids: string[] | string) => {
    const gone = new Set(Array.isArray(ids) ? ids : [ids]);
    setPending((list) => list.filter((say) => !gone.has(say.id)));
  }, []);

  useEffect(() => {
    const matched = reconciledPendingIDs(pending, frames, session.actor);
    if (matched.length > 0) dropPending(matched);
  }, [pending, frames, session.actor, dropPending]);

  // A one-flight, one-key guard per user intention: double-clicks and retries
  // reuse the same idempotency key, so at most one durable event results.
  const inFlight = useRef(new Set<string>());
  const actKeys = useRef(new RetryKeys());
  const [actError, setActError] = useState<string>();
  const doAct = useCallback(
    async (intent: string, input: Omit<ActInput, "credential" | "idempotency_key">) => {
      if (inFlight.current.has(intent)) return;
      inFlight.current.add(intent);
      setActError(undefined);
      const payload = JSON.stringify(input);
      const key = actKeys.current.forAttempt(intent, payload);
      try {
        await api.act({ ...input, credential: session.credential, idempotency_key: key });
        actKeys.current.succeeded(intent, key);
      } catch (error) {
        setActError(error instanceof Error ? error.message : String(error));
      } finally {
        inFlight.current.delete(intent);
      }
    },
    [session.credential],
  );

  return (
    <div className="flex h-full flex-col">
      <TopBar
        workroom={workroom}
        session={session}
        onJumpEvent={openThread}
        selectedEvent={screen.kind === "thread" ? screen.event : undefined}
      />
      <main className="flex min-h-0 min-w-0 flex-1 flex-col">
        {screen.kind === "list" ? (
          <RequestList workroom={workroom} onOpenThread={openThread} view={listView} onView={setListView} />
        ) : (
          <Thread
            key={screen.event}
            workroom={workroom}
            session={session}
            frames={frames}
            root={screen.event}
            pending={pending}
            onBack={showList}
            onOpenThread={openThread}
            onSay={echoSay}
            onSayFailed={dropPending}
            doAct={doAct}
            actError={actError}
          />
        )}
      </main>
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
        <p className="mt-1 text-xs text-muted">Everything you do is signed as who you choose.</p>
        <p className="mt-2 text-xs text-danger">
          {workroom.status?.trust_boundary ?? "Trusted processes only: every process inside this resident boundary can act as every actor key this application can open."}
        </p>
        <div className="mt-4 space-y-1.5">
          {workroom.actors.length === 0 && <p className="text-xs italic text-faint">Waiting for the room…</p>}
          {workroom.actors.map((actor, index) => (
            <button
              key={actor.name}
              ref={index === 0 ? first : undefined}
              onClick={() => onJoin(actor.name)}
              className="flex w-full items-center gap-3 rounded-lg border border-border px-3 py-2 text-left text-sm hover:border-accent/50 hover:bg-elevated focus-visible:outline focus-visible:outline-accent"
            >
              <Avatar fingerprint={actor.fingerprint} name={actor.name} size={28} />
              <span className="font-medium">{actor.name}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
