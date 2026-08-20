import { useEffect, useRef, useState } from "react";
import { api, type Activity, type ActivityStatus, type ActivityUpdate } from "./api";
import { renewCredential } from "./lease";

// The browser is a leased participant like any other client: it announces a
// lease bound to one actor, heartbeats it, and departs on unload. The resident
// mints the private credential; this tab keeps it only in memory.
export interface Session {
  credential: string;
  actor?: string;
  live: boolean;
  activity: Activity;
  setActor: (name: string) => void;
  setActivity: (update: ActivityUpdate) => void;
}

export function useSession(): Session {
  const [actor, setActorState] = useState<string | undefined>(
    () => localStorage.getItem("workroom.actor") ?? undefined,
  );
  const [live, setLive] = useState(false);
  const [activity, setActivityState] = useState<Activity>({ status: "available", focus: [] });
  const activityRef = useRef(activity);
  const credentialRef = useRef("");
  const effective = actor;
  const [credential, setCredential] = useState("");

  useEffect(() => {
    if (!effective) return;
    let stopped = false;
    credentialRef.current = "";
    setCredential("");
    const renew = () =>
      renewCredential(effective, credentialRef.current, api.announce)
        .then((next) => {
          if (stopped) return;
          credentialRef.current = next;
          setCredential(next);
          setLive(Boolean(next));
        })
        .catch(() => {
          if (stopped) return;
          setLive(false);
        });
    void renew();
    const timer = setInterval(renew, 15000);
    const bye = () => credentialRef.current && void api.depart(credentialRef.current);
    window.addEventListener("pagehide", bye);
    return () => {
      stopped = true;
      clearInterval(timer);
      window.removeEventListener("pagehide", bye);
      setLive(false);
      if (credentialRef.current) void api.depart(credentialRef.current);
      credentialRef.current = "";
    };
  }, [effective]);

  return {
    credential,
    actor: effective,
    live,
    activity,
    setActor: (name: string) => {
      localStorage.setItem("workroom.actor", name);
      setActorState(name);
    },
    setActivity: (update: ActivityUpdate) => {
      const status: ActivityStatus = update.status ?? activityRef.current.status;
      const next: Activity = {
        status,
        focus: update.focus === undefined ? activityRef.current.focus : [...new Set(update.focus)].sort().slice(0, 8),
        note: update.note === undefined ? activityRef.current.note : update.note,
      };
      activityRef.current = next;
      setActivityState(next);
      if (effective && credentialRef.current) {
        api.announce(effective, credentialRef.current, next).then(() => setLive(true)).catch(() => setLive(false));
      }
    },
  };
}
