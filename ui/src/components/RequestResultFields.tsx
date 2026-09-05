import type { RequestResult } from "../lib/requestResult.ts";

export function RequestResultFields({ value, onChange, repo, actors }: {
  value: RequestResult;
  onChange: (value: RequestResult) => void;
  repo?: string;
  actors: { fingerprint: string; name: string }[];
}) {
  const inputClass = "min-w-0 max-w-full rounded border border-input bg-surface px-2 py-1 text-xs outline-none focus:border-accent/60";
  const landing = value.kind === "target" || value.kind === "inherit";
  return (
    <fieldset className="space-y-2 rounded border border-border p-2">
      <legend className="px-1 text-xs font-semibold">Result owed</legend>
      <select aria-label="Result owed" value={value.kind}
        onChange={(event) => onChange({ ...value, kind: event.target.value as RequestResult["kind"], held: false, owner: "" })}
        className={inputClass}>
        <option value="">Choose a result…</option>
        <option value="target">Land a Git artifact into a named branch</option>
        <option value="inherit">Land into the target inherited from request ancestry</option>
        <option value="none">No Git artifact</option>
      </select>
      {value.kind === "target" && <div className="flex flex-col gap-1 text-xs">
        <span className="break-all text-muted">Repository: {repo ?? "unavailable"}</span>
        <label className="flex flex-col gap-1">Target branch ref
          <input value={value.ref} placeholder="refs/heads/release-2" onChange={(event) => onChange({ ...value, ref: event.target.value })} className={inputClass} />
        </label>
        <span className="text-faint">The service records the branch’s current commit when filing. A missing branch refuses the request.</span>
      </div>}
      {value.kind === "inherit" && <p className="text-xs text-muted">The service resolves request ancestry. Missing or conflicting targets refuse the request.</p>}
      {value.kind === "none" && <p className="text-xs text-muted">Completion is a report accepted by the requester.</p>}
      {landing && <div className="flex flex-wrap items-center gap-2 text-xs">
        <label className="flex items-center gap-1"><input type="checkbox" checked={value.held} onChange={(event) => onChange({ ...value, held: event.target.checked, owner: "" })} />Hold landing</label>
        {value.held && <label className="flex items-center gap-1">Hold owner
          <select value={value.owner} onChange={(event) => onChange({ ...value, owner: event.target.value })} className={inputClass}>
            <option value="">Choose an owner…</option>
            {actors.map((actor) => <option key={actor.fingerprint} value={actor.fingerprint}>{actor.name}</option>)}
          </select>
        </label>}
        <p className="w-full text-faint">{value.held ? "After approval, the hold owner must release this exact candidate before it can land." : "After approval, this result still owes a sealed landing into its target."}</p>
      </div>}
    </fieldset>
  );
}
