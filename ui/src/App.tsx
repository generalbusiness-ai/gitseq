import { useMemo, useState } from "react";
import { PanelRight } from "lucide-react";
import { useWorkroom, provenanceClosure, type Selection } from "./lib/store";
import { useSession } from "./lib/session";
import { TopBar } from "./components/TopBar";
import { Stream } from "./components/Stream";
import { Composer, type ComposerContext } from "./components/Composer";
import { RightRail } from "./components/RightRail";
import { cn } from "./lib/util";

const emptyComposer: ComposerContext = { setDown: false, restsOn: [], frames: [] };

export default function App() {
  const workroom = useWorkroom();
  const session = useSession();
  const [selection, setSelection] = useState<Selection>();
  const [composer, setComposer] = useState<ComposerContext>(emptyComposer);
  const [railOpen, setRailOpen] = useState(false);

  const projection = workroom.status?.durable.projection;
  const statements = projection?.statements ?? [];
  const highlight = useMemo(
    () => provenanceClosure(selection, projection?.provenance ?? {}, workroom.commits, statements),
    [selection, projection, workroom.commits, statements],
  );

  const select = (next: Selection) =>
    setSelection((current) =>
      current && current.kind === next.kind && current.id === next.id ? undefined : next,
    );

  return (
    <div className="flex h-full flex-col">
      <TopBar workroom={workroom} session={session} />
      <main className="relative grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(540px,1fr)_minmax(380px,430px)]">
        <div className="flex min-h-0 flex-col lg:border-r lg:border-border">
          <Stream
            workroom={workroom}
            session={session}
            highlight={highlight}
            selection={selection}
            onSelect={select}
            composer={composer}
            onComposer={setComposer}
          />
          <Composer workroom={workroom} session={session} context={composer} onContext={setComposer} />
        </div>
        <div
          className={cn(
            "min-h-0",
            "max-lg:absolute max-lg:inset-y-0 max-lg:right-0 max-lg:z-20 max-lg:w-[min(26rem,90vw)] max-lg:border-l max-lg:border-border max-lg:bg-background max-lg:shadow-2xl",
            !railOpen && "max-lg:hidden",
          )}
        >
          <RightRail workroom={workroom} highlight={highlight} selection={selection} onSelect={select} />
        </div>
      </main>
      {!session.actor && <JoinGate workroom={workroom} onJoin={session.setActor} />}
      <button
        onClick={() => setRailOpen((open) => !open)}
        aria-label="toggle side panel"
        className="fixed bottom-20 right-4 z-30 rounded-full border border-border bg-card p-2.5 text-muted shadow-lg lg:hidden"
      >
        <PanelRight className="h-4 w-4" />
      </button>
    </div>
  );
}

// Joining is a custody decision, made explicitly — never defaulted.
function JoinGate({ workroom, onJoin }: { workroom: ReturnType<typeof useWorkroom>; onJoin: (name: string) => void }) {
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-background/80 backdrop-blur-sm">
      <div className="w-96 rounded-xl border border-border bg-card p-6 shadow-2xl">
        <h2 className="font-serif text-lg font-semibold">Join the workroom</h2>
        <p className="mt-1 text-xs leading-relaxed text-muted">
          Everything you set down is signed as the identity you choose, permanently and publicly.
          This service holds the keys; choose who you are.
        </p>
        <div className="mt-4 space-y-1.5">
          {workroom.actors.length === 0 && <p className="text-xs italic text-faint">Waiting for the room…</p>}
          {workroom.actors.map((actor) => (
            <button
              key={actor.name}
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
