import { useMemo, useState } from "react";
import { useWorkroom, provenanceClosure, type Selection } from "./lib/store";
import { useSession } from "./lib/session";
import { TopBar } from "./components/TopBar";
import { Stream } from "./components/Stream";
import { Composer, type ComposerContext } from "./components/Composer";
import { RightRail } from "./components/RightRail";

const emptyComposer: ComposerContext = { mode: "say", restsOn: [], frames: [] };

export default function App() {
  const workroom = useWorkroom();
  const session = useSession(workroom.actors[0]?.name);
  const [selection, setSelection] = useState<Selection>();
  const [composer, setComposer] = useState<ComposerContext>(emptyComposer);

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
      <main className="grid min-h-0 flex-1 grid-cols-[minmax(540px,1fr)_minmax(380px,430px)]">
        <div className="flex min-h-0 flex-col border-r border-border">
          <Stream
            workroom={workroom}
            highlight={highlight}
            selection={selection}
            onSelect={select}
            session={session}
            composer={composer}
            onComposer={setComposer}
          />
          <Composer workroom={workroom} session={session} context={composer} onContext={setComposer} />
        </div>
        <RightRail workroom={workroom} highlight={highlight} selection={selection} onSelect={select} />
      </main>
    </div>
  );
}
