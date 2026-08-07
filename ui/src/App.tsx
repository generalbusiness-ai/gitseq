import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useWorkroom, provenanceClosure, foldAnchor, type Selection } from "./lib/store";
import { useSession } from "./lib/session";
import { useFrames } from "./lib/frames";
import { api, type ActInput } from "./lib/api";
import { TopBar } from "./components/TopBar";
import { Stream, type PendingSay } from "./components/Stream";
import { Composer, emptyComposer, type ComposerContext } from "./components/Composer";
import { WorkDrawer } from "./components/WorkDrawer";
import { ThreadPane, type ThreadRoute, type ThreadTarget } from "./components/ThreadPane";
import { ProfilePane } from "./components/ProfilePane";
import { Avatar } from "./components/Avatar";
import { RetryKeys, threadTargetKey } from "./lib/interaction";

// One right-hand slot, Slack-style: the thread pane, a profile, or the Work
// drawer — whichever opened last wins; Escape closes it.
type Pane =
  | { kind: "work" }
  | { kind: "thread"; target: ThreadTarget; route?: ThreadRoute }
  | { kind: "profile"; fingerprint: string }
  | undefined;

// The Room is the only permanent center; everything else lives in the pane.
export default function App() {
  const workroom = useWorkroom();
  const session = useSession();
  const { frames, deliveries } = useFrames(workroom);
  const [selection, setSelection] = useState<Selection>();
  const [composer, setComposer] = useState<ComposerContext>(emptyComposer);
  const [pane, setPane] = useState<Pane>();
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

  // Jump the stream to a durable event; folded promise/report events land on
  // their request's card.
  const jumpToEvent = useCallback(
    (event: string) => {
      setSelection({ kind: "event", id: event });
      const anchor = foldAnchor(event, projection);
      requestAnimationFrame(() => document.getElementById("evt-" + anchor)?.scrollIntoView({ block: "center" }));
    },
    [projection],
  );

  const echoSay = useCallback((text: string, re?: string) => {
    const id = crypto.randomUUID();
    setPending((list) => [...list, { id, text, at: Date.now(), re }]);
    return id;
  }, []);
  const dropPending = useCallback(
    (ids: string[] | string) => {
      const gone = new Set(Array.isArray(ids) ? ids : [ids]);
      setPending((list) => list.filter((p) => !gone.has(p.id)));
    },
    [],
  );

  // A one-flight, one-key guard per user intention: double-clicks and retries
  // reuse the same idempotency key, so at most one durable event results.
  const inFlight = useRef(new Set<string>());
  const actKeys = useRef(new RetryKeys());
  const [actError, setActError] = useState<string>();
  const doAct = useCallback(
    async (intent: string, input: Omit<ActInput, "session" | "idempotency_key">) => {
      if (inFlight.current.has(intent)) return;
      inFlight.current.add(intent);
      setActError(undefined);
      const payload = JSON.stringify(input);
      const key = actKeys.current.forAttempt(intent, payload);
      try {
        await api.act({ ...input, session: session.id, idempotency_key: key });
        actKeys.current.succeeded(intent, key);
      } catch (error) {
        setActError(error instanceof Error ? error.message : String(error));
      } finally {
        inFlight.current.delete(intent);
      }
    },
    [session.id],
  );

  // Every text-bearing response opens the target's thread composer. The room
  // composer remains available beside it, following the familiar Slack model.
  const route = useCallback(
    (mode: ThreadRoute["mode"], basis: string, prefill: string) =>
      setPane({
        kind: "thread",
        target: { kind: "event", event: basis },
        route: { id: crypto.randomUUID(), mode, prefill },
      }),
    [],
  );

  const openThread = useCallback((target: ThreadTarget) => setPane({ kind: "thread", target }), []);
  const openProfile = useCallback((fingerprint: string) => fingerprint && setPane({ kind: "profile", fingerprint }), []);
  const closePane = useCallback(() => setPane(undefined), []);

  return (
    <div className="flex h-full flex-col">
      <TopBar
        workroom={workroom}
        session={session}
        onOpenWork={() => setPane({ kind: "work" })}
        onJumpEvent={jumpToEvent}
        onOpenProfile={openProfile}
      />
      <div className="flex min-h-0 flex-1">
        <main className="flex min-h-0 min-w-0 flex-1 flex-col">
          <Stream
            workroom={workroom}
            session={session}
            frames={frames}
            deliveries={deliveries}
            highlight={highlight}
            selection={selection}
            onSelect={select}
            onJump={jump}
            composer={composer}
            onComposer={setComposer}
            pending={pending}
            onReconcile={dropPending}
            onOpenThread={openThread}
            onRoute={route}
            onOpenProfile={openProfile}
            doAct={doAct}
            actError={actError}
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
        {pane?.kind === "thread" && (
          <ThreadPane
            key={`${threadTargetKey(pane.target)}:${pane.route?.id ?? "plain"}`}
            workroom={workroom}
            session={session}
            frames={frames}
            target={pane.target}
            route={pane.route}
            pending={pending}
            composer={composer}
            onComposer={setComposer}
            onClose={closePane}
            onJumpTo={jumpToEvent}
            onOpenProfile={openProfile}
            onRoute={route}
            doAct={doAct}
            onSay={echoSay}
            onSayFailed={dropPending}
          />
        )}
        {pane?.kind === "profile" && (
          <ProfilePane
            workroom={workroom}
            session={session}
            fingerprint={pane.fingerprint}
            onClose={closePane}
            onJumpTo={jumpToEvent}
          />
        )}
      </div>
      {pane?.kind === "work" && (
        <WorkDrawer
          workroom={workroom}
          highlight={highlight}
          selection={selection}
          onSelect={select}
          onJump={jump}
          onClose={closePane}
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
        <p className="mt-1 text-xs text-muted">Everything you do is signed as who you choose.</p>
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
