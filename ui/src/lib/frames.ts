import { useEffect, useMemo, useRef, useState } from "react";
import { api, decodeFrame, type Frame, type FrameView } from "./api";
import type { Workroom } from "./store";
import { fingerprintOfKey } from "./util";

// One shared source of live chat frames for the stream and the thread pane.
// Each frame is decoded once, its author resolved by recomputing the sha256
// fingerprint of the signing key, and stamped with the moment this browser
// first saw it — arrival time, honestly labeled.
export function useFrames(workroom: Workroom): { frames: FrameView[]; deliveries: number } {
  const conversations = workroom.status?.live.conversations ?? [];
  const livePosition = workroom.status?.live.cursor.position ?? 0;
  const [frames, setFrames] = useState<FrameView[]>([]);
  const firstSeen = useRef(new Map<string, number>());
  // Counts completed deliveries: the first one is the state of the room as we
  // opened it, so nothing in it should count as news (title-knock upstream).
  const deliveries = useRef(0);

  const byFingerprint = useMemo(() => new Map(workroom.actors.map((a) => [a.fingerprint, a.name])), [workroom.actors]);

  useEffect(() => {
    let stopped = false;
    Promise.all(
      conversations.map((c) =>
        api
          .frames(c)
          .then(async (raw: Frame[]) =>
            Promise.all(
              raw.map(async (frame) => {
                const view = decodeFrame(frame);
                const fp = await fingerprintOfKey(frame.ActorKey).catch(() => "");
                const key = `${view.conversation}:${view.sequence}`;
                if (!firstSeen.current.has(key)) firstSeen.current.set(key, Date.now());
                return {
                  ...view,
                  fingerprint: fp,
                  actor: byFingerprint.get(fp) ?? view.actor,
                  seen: firstSeen.current.get(key)!,
                };
              }),
            ),
          )
          .catch(() => [] as FrameView[]),
      ),
    ).then((groups) => {
      if (stopped) return;
      deliveries.current += 1;
      setFrames(groups.flat());
    });
    return () => {
      stopped = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversations.join(","), livePosition, byFingerprint]);

  return { frames, deliveries: deliveries.current };
}
