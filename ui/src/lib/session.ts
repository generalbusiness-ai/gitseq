import { useEffect, useMemo, useState } from "react";
import { api } from "./api";

// The browser is a leased participant like any other client: it announces a
// session bound to one actor, heartbeats the lease, and departs on unload.
// Sessions bind to exactly one actor, so switching actor mints a new session.
export interface Session {
  id: string;
  actor?: string;
  live: boolean;
  setActor: (name: string) => void;
}

function mintSessionID(): string {
  const bytes = new Uint8Array(9);
  crypto.getRandomValues(bytes);
  return "web:" + Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

export function useSession(): Session {
  const [actor, setActorState] = useState<string | undefined>(
    () => localStorage.getItem("workroom.actor") ?? undefined,
  );
  const [live, setLive] = useState(false);
  const effective = actor;
  const id = useMemo(mintSessionID, [effective]);

  useEffect(() => {
    if (!effective) return;
    let stopped = false;
    const renew = () =>
      api
        .announce(effective, id)
        .then(() => !stopped && setLive(true))
        .catch(() => !stopped && setLive(false));
    void renew();
    const timer = setInterval(renew, 15000);
    const bye = () => void api.depart(id);
    window.addEventListener("pagehide", bye);
    return () => {
      stopped = true;
      clearInterval(timer);
      window.removeEventListener("pagehide", bye);
      setLive(false);
      void api.depart(id);
    };
  }, [effective, id]);

  return {
    id,
    actor: effective,
    live,
    setActor: (name: string) => {
      localStorage.setItem("workroom.actor", name);
      setActorState(name);
    },
  };
}
