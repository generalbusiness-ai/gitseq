import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useWorkroom } from "./lib/store";
import { buildRecordIndex } from "./lib/records";
import { useSession } from "./lib/session";
import { useFrames } from "./lib/frames";
import { api, type ActInput } from "./lib/api";
import { TopBar } from "./components/TopBar";
import { RequestList, defaultListView, type ListView } from "./components/RequestList";
import { Thread, type PendingSay } from "./components/Thread";
import { PublishArtifact, type PublishInput } from "./components/Publish";
import { Avatar } from "./components/Avatar";
import { publishRefusal, signingRefusal } from "./lib/authority";
import { reconciledPendingIDs, RetryKeys } from "./lib/interaction";
import { firstLine } from "./lib/util";
import { tabTitle } from "./lib/title";

// Two screens. The list is the default and answers the whole question; the
// thread answers "what does this one wait on?". There is no third
// destination, and no presentation to choose between. A thread carries the
// record the user clicked as focus when navigation resolved it into a wider
// commitment, so arrival can name what was asked for.
type Screen = { kind: "list" } | { kind: "thread"; event: string; focus?: string };

export default function App() {
  const workroom = useWorkroom();
  useEffect(() => {
    document.title = tabTitle(workroom.project);
  }, [workroom.project]);
  const session = useSession();
  const { frames } = useFrames(workroom);
  const [screen, setScreen] = useState<Screen>({ kind: "list" });
  const [listView, setListView] = useState<ListView>(defaultListView);
  // Optimistic say echoes: appended on send, reconciled when the frame lands.
  const [pending, setPending] = useState<PendingSay[]>([]);

  const projection = workroom.status?.durable.projection;

  // Any record opens the thread it belongs to, resolved one way for every
  // caller: a promise, report or act lands on the commitment it answers, and
  // a record belonging to no commitment — a proposal, a free-standing
  // assert — opens as itself. The clicked record rides along as focus.
  const index = useMemo(() => (projection ? buildRecordIndex(projection) : undefined), [projection]);
  const openThread = useCallback(
    (event: string) => {
      const root = index ? index.threadRoot(event) : event;
      setScreen({ kind: "thread", event: root, focus: root === event ? undefined : event });
    },
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

  const myFingerprint = workroom.actors.find((actor) => actor.name === session.actor)?.fingerprint;

  // A one-flight, one-key guard per user intention: double-clicks and retries
  // reuse the same idempotency key, so at most one durable event results.
  const inFlight = useRef(new Set<string>());
  const actKeys = useRef(new RetryKeys());
  const [actError, setActError] = useState<string>();
  const doAct = useCallback(
    async (intent: string, input: Omit<ActInput, "credential" | "idempotency_key">) => {
      if (inFlight.current.has(intent)) return;
      // The authority question, asked here rather than only where the control
      // was drawn. Toolbar decides what to offer when a row renders; between
      // that render and this signature a lease can expire and a role can be
      // superseded, and the fold judges the record by what is true now. This
      // reads the current projection, not the one the offer was drawn from.
      const denied = signingRefusal(input, {
        live: session.live,
        actors: projection?.actors ?? {},
        me: myFingerprint || undefined,
        target: input.target ? index?.statement(input.target) : undefined,
        // The fold refuses a ratification whose target it has not ruled
        // effective, and effectiveness is not on the statement — it is in
        // `decisions`. Resolving it here is what lets the guard fail closed.
        targetDecision: input.target ? index?.decision(input.target) : undefined,
        originatingRequester: input.target ? index?.commitment(input.target)?.requester : undefined,
      });
      if (denied) {
        setActError(`not filed: ${denied}`);
        return;
      }
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
    [session.credential, session.live, projection, index, myFingerprint],
  );

  // Publish authority, asked once here and read everywhere the publish path
  // can still be stopped: the top bar's control, the dialog's submit, and the
  // signing boundary below. It is a fact about this render, not about the
  // moment the dialog opened — a lease can expire and a membership grant can
  // be superseded while the form is on screen, and the fold refuses a state
  // record from a signer who is not a live participant whenever that happens.
  const publishDenied = publishRefusal(session.live, projection?.actors ?? {}, myFingerprint || undefined);

  // Publishing an artifact is the one act that starts from nothing, so it is
  // the one the two screens cannot hold. Opened from a thread it offers that
  // thread's record as a basis, which is how a stamped predecessor comes to
  // rest on its replacement.
  const [publishing, setPublishing] = useState(false);
  const [filing, setFiling] = useState(false);
  const [awaiting, setAwaiting] = useState<string>();
  const [publishError, setPublishError] = useState<string>();

  // The record is signed and appended before the fold has projected it. Opening
  // its thread in that gap renders "this thread is not in the projection",
  // which is a false negative about a record we just filed successfully — so
  // the arrival waits for the projection to carry it.
  useEffect(() => {
    if (!awaiting || !index?.has(awaiting)) return;
    setAwaiting(undefined);
    setPublishing(false);
    openThread(awaiting);
  }, [awaiting, index, openThread]);

  const publish = useCallback(
    async (input: PublishInput) => {
      if (filing || awaiting) return;
      // The load-bearing check, and the last one. Every gate before this is a
      // courtesy drawn on a screen: a disabled button stops a click, and
      // stops nothing else. This runs in the same statement sequence as the
      // signature, with the authority read at this render rather than the one
      // that opened the dialog, so a session that expired or a membership that
      // was superseded in between refuses here — with the reason on screen —
      // instead of appending a permanently ineffective row to an append-only
      // log.
      if (publishDenied) {
        setPublishError(`not filed: ${publishDenied}`);
        return;
      }
      setFiling(true);
      setPublishError(undefined);
      const act = {
        credential: session.credential,
        act: "state" as const,
        kind: "artifact",
        text: input.text,
        body: { path: input.path, commit: input.commit },
        rests_on: input.basis ? [input.basis] : [],
      };
      const key = actKeys.current.forAttempt("publish", JSON.stringify(act));
      try {
        const { id } = await api.act({ ...act, idempotency_key: key });
        actKeys.current.succeeded("publish", key);
        setAwaiting(id);
      } catch (error) {
        setPublishError(error instanceof Error ? error.message : String(error));
      } finally {
        setFiling(false);
      }
    },
    [filing, awaiting, session.credential, publishDenied],
  );

  const publishBasis =
    screen.kind === "thread"
      ? { event: screen.event, label: firstLine(index?.statement(screen.event)?.text ?? screen.event, 60) }
      : undefined;

  return (
    <div className="flex h-full flex-col">
      <TopBar
        workroom={workroom}
        session={session}
        onJumpEvent={openThread}
        onPublish={() => {
          setPublishError(undefined);
          setPublishing(true);
        }}
        selectedEvent={screen.kind === "thread" ? screen.event : undefined}
      />
      <main className="flex min-h-0 min-w-0 flex-1 flex-col">
        {screen.kind === "list" || !index ? (
          <RequestList workroom={workroom} onOpenThread={openThread} view={listView} onView={setListView} />
        ) : (
          <Thread
            index={index}
            key={`${screen.event}:${screen.focus ?? ""}`}
            workroom={workroom}
            session={session}
            frames={frames}
            root={screen.event}
            focus={screen.focus}
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
      {publishing && (
        <PublishArtifact
          basis={publishBasis}
          busy={filing || awaiting !== undefined}
          refusal={publishDenied}
          error={publishError}
          onPublish={publish}
          onClose={() => {
            setPublishing(false);
            setAwaiting(undefined);
            setPublishError(undefined);
          }}
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
