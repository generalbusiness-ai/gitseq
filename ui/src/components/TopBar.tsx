import { useEffect, useState } from "react";
import { AtSign } from "lucide-react";
import { forYouItems, ticketsOf, type Workroom } from "../lib/store";
import type { Session } from "../lib/session";
import { loadForYouWatermark, saveForYouWatermark } from "../lib/memory";
import { cn } from "../lib/util";
import { repoRemoteHref } from "../lib/repolink";
import { Avatar } from "./Avatar";
import { fingerprintOfPresentActor, presentActors, toggleActivityFocus } from "../lib/interaction";
import { publishRefusal } from "../lib/authority";

export function TopBar({
  workroom,
  session,
  onJumpEvent,
  onPublish,
  selectedEvent,
}: {
  workroom: Workroom;
  session: Session;
  onJumpEvent: (event: string) => void;
  /**
   * Opens the artifact dialog. It lives here because publishing answers no
   * request and replies to no thread, so neither screen owns it — and because
   * from a thread it can offer that thread's record as the pointer's basis.
   */
  onPublish: () => void;
  /** The thread that is open, if one is: what advisory focus would name. */
  selectedEvent?: string;
}) {
  const durable = workroom.status?.durable;
  const people = presentActors(workroom.status?.live.presence, workroom.status?.live.activity);
  const tickets = ticketsOf(durable?.projection);
  const selectedFocused = Boolean(selectedEvent && session.activity?.focus.includes(selectedEvent));
  const fingerprintOf = (name: string) => workroom.actors.find((a) => a.name === name)?.fingerprint ?? "";
  // The served path names the repository; its remote, when there is one this is
  // willing to link, says where that repository lives. Anything the allowlist
  // refuses leaves this undefined and the path renders as it always has.
  const repoHref = repoRemoteHref(workroom.repoRemote);

  // "For you": durable acts addressed to me since the stored watermark.
  // Clicking steps to the oldest unseen one and marks it read; each click
  // advances one act, so nothing addressed to you can be skipped unseen.
  const genesis = durable?.genesis ?? "";
  const myFingerprint = fingerprintOf(session.actor ?? "");
  const [watermark, setWatermark] = useState(0);
  useEffect(() => {
    setWatermark(loadForYouWatermark(genesis, myFingerprint));
  }, [genesis, myFingerprint]);
  const unseen = forYouItems(durable?.projection, myFingerprint || undefined, watermark);

  // Publishing an artifact is a state write, and the fold refuses a state
  // record from a signer who is not a live participant. So the button asks two
  // questions and needs both answered yes. Presence says a session is open to
  // sign with; participation says the fold will accept what it signs. Neither
  // implies the other: a departed principal keeps its key and can still open a
  // session, and a member who has gone home is not here to press anything.
  // Asking only presence — which is advisory and session-bound — invited a
  // departed actor to append a permanently ineffective row to an append-only
  // log. Neither question is answered here: `publishRefusal` is the shared
  // rule, asked again by the dialog's submit gate and by the signing boundary
  // in `App.tsx`, so this control cannot drift from what actually decides.
  //
  // The claim made here is about this control's full two-part refusal. The
  // shared `isLiveParticipant` predicate also withholds the seven Toolbar
  // routes that open ordinary state composers. Direct ratification instead
  // follows `mayRatify` and the target's admitted satisfier, while own-author
  // `withdraw` remains deliberately outside the participant rule because it
  // signs supersession rather than state.
  //
  // Opening the dialog is not the last chance to refuse, and this control is
  // not the guarantee: authority can be lost while the dialog is open, so the
  // same rule is asked again at the moment of signing.
  const refusal = publishRefusal(session.live, durable?.projection?.actors ?? {}, myFingerprint || undefined);
  // Disabled with a reason rather than hidden: a control that vanishes teaches
  // nothing, and the two refusals are different facts, so they carry different
  // words.
  const publishTitle = refusal ?? "Publish an artifact: a signed pointer to one path at one exact commit";
  const readOldest = () => {
    const oldest = unseen[0];
    if (!oldest) return;
    onJumpEvent(oldest.event);
    saveForYouWatermark(genesis, myFingerprint, oldest.ticket);
    setWatermark(oldest.ticket);
  };

  return (
    <header className="flex items-center justify-between gap-3 border-b border-border px-3 py-2.5 sm:gap-6 sm:px-6">
      <div className="flex min-w-0 flex-1 items-baseline gap-3">
        <h1 className="shrink-0 font-serif text-lg font-semibold tracking-tight sm:text-xl">The Workroom</h1>
        {workroom.repo &&
          (repoHref ? (
            <a
              href={repoHref}
              target="_blank"
              rel="noopener noreferrer"
              className="truncate font-mono text-xs text-faint hover:text-muted hover:underline focus-visible:outline focus-visible:outline-accent"
              title={workroom.repo}
            >
              {workroom.repo}
            </a>
          ) : (
            <span className="truncate font-mono text-xs text-faint" title={workroom.repo}>
              {workroom.repo}
            </span>
          ))}
      </div>
      <div className="flex shrink-0 items-center gap-2 sm:gap-4">
        {session.actor && (
          <div className="hidden items-center gap-1 md:flex" aria-label="Advisory activity">
            <select
              aria-label="Activity status"
              value={session.activity?.status ?? "available"}
              onChange={(event) => session.setActivity({ status: event.target.value as import("../lib/api").ActivityStatus })}
              className="h-7 rounded border border-border bg-background px-1.5 text-[11px] text-muted outline-none focus:border-accent/60"
            >
              <option value="available">available</option>
              <option value="busy">busy</option>
              <option value="waiting">waiting</option>
              <option value="blocked">blocked</option>
            </select>
            {selectedEvent && (
              <button
                type="button"
                aria-pressed={selectedFocused}
                title="Advisory focus only; this does not claim or complete work"
                onClick={() => session.setActivity({ focus: toggleActivityFocus(session.activity.focus, selectedEvent) })}
                className={cn("h-7 rounded border px-1.5 text-[11px] focus-visible:outline focus-visible:outline-accent", selectedFocused ? "border-accent/60 bg-accent/10 text-accent" : "border-border text-muted")}
              >
                {selectedFocused ? "unfocus" : `focus #${tickets.get(selectedEvent) ?? "?"}`}
              </button>
            )}
            {(session.activity?.focus.length ?? 0) > 0 && (
              <button type="button" onClick={() => session.setActivity({ focus: [] })} className="h-7 rounded px-1 text-[11px] text-faint hover:text-muted focus-visible:outline focus-visible:outline-accent">
                clear
              </button>
            )}
          </div>
        )}
        <div className="hidden items-center -space-x-1.5 sm:flex">
          {people.length === 0 ? (
            <span className="text-xs text-faint">nobody here</span>
          ) : (
            people.map((person) => {
              const fingerprint = fingerprintOfPresentActor(person, workroom.actors);
              return (
                <span key={person.label} className="relative">
                  <Avatar
                    fingerprint={fingerprint}
                    name={person.name}
                    title={`${person.name} — ${person.status}${person.note ? ` — ${person.note}` : ""}${person.sessions > 1 ? ` — ${person.sessions} sessions` : ""}`}
                    size={24}
                    className="ring-2 ring-background"
                  />
                  {person.sessions > 1 && (
                    <span
                      aria-hidden
                      className="pointer-events-none absolute -bottom-0.5 -right-0.5 rounded-full bg-elevated px-1 font-mono text-[9px] leading-[1.3] text-muted ring-1 ring-background"
                    >
                      {person.sessions}
                    </span>
                  )}
                </span>
              );
            })
          )}
        </div>
        {unseen.length > 0 && (
          <button
            onClick={readOldest}
            title="for you"
            className="flex items-center gap-1 rounded-md border border-accent/50 bg-accent/10 px-2 py-0.5 text-xs font-medium text-accent transition-colors hover:bg-accent/20 focus-visible:outline focus-visible:outline-accent"
          >
            <AtSign className="h-3 w-3" />
            {unseen.length}
            <span className="hidden sm:inline"> for you</span>
          </button>
        )}
        {session.actor && (
          <button
            type="button"
            onClick={onPublish}
            disabled={refusal !== undefined}
            title={publishTitle}
            className="rounded-md border border-border px-2 py-0.5 text-xs text-muted hover:border-accent/50 hover:text-foreground focus-visible:outline focus-visible:outline-accent disabled:opacity-40"
          >
            publish
          </button>
        )}
        {session.actor && (
          <span title="you" className="flex items-center gap-1.5 rounded-md border border-border px-1.5 py-0.5 text-xs text-foreground/85">
            <Avatar fingerprint={myFingerprint} name={session.actor} size={18} />
            {session.actor}
          </span>
        )}
        {workroom.offline && (
          <span role="alert" className="text-xs text-danger">
            offline
          </span>
        )}
      </div>
    </header>
  );
}
