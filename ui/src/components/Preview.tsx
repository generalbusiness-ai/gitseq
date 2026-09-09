import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { api, type PreviewResponse } from "../lib/api";
import { formatAddress, type Address, type PreviewAddress } from "../lib/address";
import { FILE_TOKEN, referenceTarget, safeExternalLink, inlineTokens } from "../lib/fileReferences";
import { useModalFocus } from "../lib/modalFocus";

export const PreviewContext = createContext<{ address: Address; open: (target: PreviewAddress) => void } | undefined>(undefined);
export function PreviewLink({ target, children }: { target: PreviewAddress; children: ReactNode }) {
  const context = useContext(PreviewContext);
  if (!context) return <span>{children}</span>;
  return <a href={formatAddress({ ...context.address, preview: target })} className="break-words text-accent underline focus-visible:outline focus-visible:outline-accent"
    onClick={(event) => { if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && event.button === 0) { event.preventDefault(); context.open(target); } }}>{children}</a>;
}

function Reference({ value, event, children, commit, basePath, attachments }: { value: string; event: string; children: ReactNode; commit?: string; basePath?: string; attachments?: string[] }) {
  const target = referenceTarget(value, event, { commit, basePath, attachments });
  if (!target && safeExternalLink(value)) return <a href={value} target="_blank" rel="noopener noreferrer" className="text-accent underline">{children}</a>;
  if (!target) return <span>{children} <span className="text-muted">(link unavailable)</span></span>;
  return <PreviewLink target={target}>{children}</PreviewLink>;
}

// React creates all text nodes. HTML, image fetching and executable Markdown
// are deliberately absent; unsupported formatting stays readable as text.
export function ReferenceText({ text, event, commit, basePath, attachments }: { text: string; event: string; commit?: string; basePath?: string; attachments?: string[] }) {
  const output: ReactNode[] = [];
  const prose = (value: string, start: number) => {
    let offset = 0;
    for (const match of value.matchAll(/\S+/g)) {
      const token = match[0].replace(/^[(["']+/, "").replace(/[),.;!?"']+$/, "");
      if (token.length > 2048 || !new RegExp(`^(?:${FILE_TOKEN.source})$`).test(token)) continue;
      const target = referenceTarget(token, event, { commit, attachments });
      if (!target) continue;
      const position = match.index! + match[0].indexOf(token);
      output.push(value.slice(offset, position));
      output.push(<PreviewLink key={`${start}:${position}`} target={target}>{token}</PreviewLink>);
      offset = position + token.length;
    }
    output.push(value.slice(offset));
  };
  let offset = 0;
  for (const token of inlineTokens(text)) {
    prose(text.slice(offset, token.start), offset);
    if (token.code !== undefined) {
      const value = token.code;
      const target = value.length <= 2048 && new RegExp(`^(?:${FILE_TOKEN.source})$`).test(value) ? referenceTarget(value, event, { commit, attachments }) : undefined;
      output.push(<code key={token.start} className="rounded bg-elevated px-1 font-mono">{target ? <PreviewLink target={target}>{value}</PreviewLink> : value}</code>);
    } else {
      output.push(token.image ? <span key={token.start}>[Image: {token.label || "unnamed"}; not loaded]</span>
        : <Reference key={token.start} value={token.url!} event={event} commit={commit} basePath={basePath} attachments={attachments}>{token.label || token.url}</Reference>);
    }
    offset = token.end;
  }
  prose(text.slice(offset), offset);
  return <>{output}</>;
}

export function MarkdownPreview({ text, event, commit, path, attachments }: { text: string; event: string; commit?: string; path?: string; attachments?: string[] }) {
  const lines = text.split("\n");
  const blocks: ReactNode[] = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*```/.test(line)) {
      const start = i; const code: string[] = [];
      while (++i < lines.length && !/^\s*```/.test(lines[i])) code.push(lines[i]);
      blocks.push(<pre key={start} className="my-3 overflow-x-auto rounded bg-elevated p-3 text-xs"><code>{code.join("\n")}</code></pre>);
    } else if (!line.trim()) blocks.push(<div key={i} className="h-2" />);
    else {
      const heading = /^(#{1,6})\s+(.+)$/.exec(line);
      const list = /^\s*(?:[-*+] |\d+\. )(.+)$/.exec(line);
      const content = <ReferenceText text={heading?.[2] ?? list?.[1] ?? line} event={event} commit={commit} basePath={path} attachments={attachments} />;
      blocks.push(heading ? <div key={i} role="heading" aria-level={heading[1].length} className="mb-1 mt-4 font-serif text-lg font-semibold">{content}</div>
        : <p key={i} className={`whitespace-pre-wrap break-words leading-relaxed ${list ? "ml-4" : ""}`}>{list && "• "}{content}</p>);
    }
  }
  return <div className="text-sm">{blocks}</div>;
}

// One record's own attachment listing: the evidence row and every reference
// in that record's text read this one answer, so a name means the same thing
// in both places.
export interface EvidenceListing { key: string; result?: PreviewResponse; error?: boolean }

// The listing is bounded per record: one `api.preview({ event })` call, never
// one per token and never a walk of history. Answers are keyed by the exact
// event, as Preview keys by its request, so a listing that arrives after the
// reader has moved to another record binds to nothing and its references stay
// as they were.
export function useEvidenceListing(event?: string): EvidenceListing {
  const [loaded, setLoaded] = useState<EvidenceListing>();
  useEffect(() => {
    if (!event) return;
    const abort = new AbortController(); setLoaded(undefined);
    api.preview({ event }, abort.signal)
      .then((result) => { if (!abort.signal.aborted) setLoaded({ key: event, result }); })
      .catch(() => { if (!abort.signal.aborted) setLoaded({ key: event, error: true }); });
    return () => abort.abort();
  }, [event]);
  return event && loaded && loaded.key === event ? loaded : { key: event ?? "" };
}

// The names a record's own text may resolve to an attachment: none until the
// listing is ready, and none when it failed or the resident refused, so prose
// falls back to source rather than flashing to a wrong target.
export function listedAttachments(listing: EvidenceListing): string[] | undefined {
  return listing.result?.status === "ready" ? listing.result.attachments : undefined;
}

export function EvidenceLinks({ event, listing }: { event: string; listing: EvidenceListing }) {
  // A listing keyed to another record says nothing about this one.
  const { result, error } = listing.key === event ? listing : { result: undefined, error: undefined };
  if (error) return <span>Evidence could not be checked. <PreviewLink target={{ event }}>Try opening evidence</PreviewLink></span>;
  if (!result) return <span>Checking evidence…</span>;
  if (result.status !== "ready") return <span>{result.message}</span>;
  if (!result.attachments?.length) return <span>No evidence attachments.</span>;
  return <ul>{result.attachments.map((attachment) => <li key={attachment}><PreviewLink target={{ event, attachment }}>{attachment}</PreviewLink></li>)}
    {!!result.omitted && <li>{result.omitted} more attachments exceed the listing limit.</li>}</ul>;
}

export function Preview({ target, onClose }: { target: PreviewAddress; onClose: () => void }) {
  const dialog = useModalFocus<HTMLDivElement>(onClose);
  const requestKey = JSON.stringify(target);
  const [loaded, setLoaded] = useState<{ key: string; value: PreviewResponse }>();
  const result = loaded?.key === requestKey ? loaded.value : undefined;
  const [error, setError] = useState<string>();
  const [source, setSource] = useState(Boolean(target.line));
  const selected = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const abort = new AbortController(); setLoaded(undefined); setError(undefined); setSource(Boolean(target.line));
    dialog.current?.querySelector<HTMLButtonElement>("button")?.focus();
    // The cited line and window start go to the resident, which chooses the
    // window; a target that changes before the answer arrives is abandoned.
    api.preview(target, abort.signal).then((value) => { if (!abort.signal.aborted) setLoaded({ key: requestKey, value }); }).catch((error) => { if (!abort.signal.aborted) setError(String(error)); });
    return () => abort.abort();
  }, [target.event, target.path, target.commit, target.attachment, target.line, target.start]);
  useEffect(() => { selected.current?.scrollIntoView?.({ block: "center" }); }, [result, source, target.line]);
  const markdown = /\.(md|mdx)$/i.test(result?.path ?? "");
  const content = result?.content ?? "";
  const window = result?.window;
  // Markdown reads as a document only when the whole file is here; a window
  // is source with real line numbers, starting at the window's first line.
  const whole = !window?.partial;
  const first = window?.start ?? 1;
  // Rows are the lines the window says it carries: none for an empty file
  // or an empty window, and a whole file's trailing newline ends its last
  // line rather than starting an empty one.
  const shown = window
    ? (window.end < window.start || window.total === 0 ? [] : (whole ? content.replace(/\n$/, "") : content).split("\n"))
    : (content === "" ? [] : content.replace(/\n$/, "").split("\n"));
  return <div className="fixed inset-0 z-40 flex items-center justify-center bg-background/80 p-2 backdrop-blur-sm sm:p-6">
    <div ref={dialog} role="dialog" aria-modal="true" aria-labelledby="preview-title" tabIndex={-1} className="flex max-h-full w-full max-w-5xl flex-col rounded-xl border border-border bg-card shadow-2xl">
      <header className="border-b border-border p-4">
        <button type="button" onClick={onClose} className="float-right rounded border border-input px-3 py-1 text-sm focus-visible:outline focus-visible:outline-accent">Close preview</button>
        <h2 id="preview-title" className="break-all font-serif text-lg">{target.attachment ?? target.path ?? "Evidence attachments"}</h2>
        <p className="mt-2 break-all text-xs text-muted">Record: {target.event}</p>
        {result && <><p className="break-all text-xs text-muted">Repository: {result.repo}</p>{result.commit && <p className="break-all text-xs text-muted">Exact revision: {result.commit}</p>}</>}
        {window && <p className="text-xs text-muted" aria-label="Window">{window.partial
          ? `Lines ${window.start.toLocaleString()}–${window.end.toLocaleString()} of ${window.total.toLocaleString()} · partial view of this file`
          : `${window.total.toLocaleString()} lines, whole file`}
          {window.previous ? <> · <PreviewLink target={{ ...target, start: window.previous }}>Previous lines</PreviewLink></> : null}
          {window.next ? <> · <PreviewLink target={{ ...target, start: window.next }}>Next lines</PreviewLink></> : null}</p>}
      </header>
      <div className="min-h-0 overflow-auto p-4">
        {!result && !error && <p role="status">Loading exact content…</p>}
        {error && <p role="alert">Preview unavailable: {error}</p>}
        {result?.message && <p role="status">{result.message}</p>}
        {result?.status === "ambiguous" && <ul>{result.heads?.map((commit) => <li key={commit}><PreviewLink target={{ ...target, commit }}>{commit}</PreviewLink></li>)}</ul>}
        {result?.status === "directory" && <ul>{result.entries?.map((name) => <li key={name}><PreviewLink target={{ event: target.event, commit: result.commit, path: `${target.path}/${name}` }}>{name}</PreviewLink></li>)}{!!result.omitted && <li>{result.omitted} more entries exceed the listing limit.</li>}</ul>}
        {result?.status === "ready" && !target.path && !target.attachment && <EvidenceLinks event={target.event} listing={{ key: target.event, result }} />}
        {result?.status === "ready" && (target.path || target.attachment) && <>
          {markdown && whole && <button type="button" className="mb-3 text-sm text-accent underline" onClick={() => setSource(!source)}>{source ? "Read Markdown" : "Show source and line numbers"}</button>}
          {!!window?.truncated?.length && <p role="status">{window.truncated.length === 1 ? `Line ${window.truncated[0]} is` : `Lines ${window.truncated.join(", ")} are`} longer than 4 KiB and shown cut.</p>}
          {markdown && whole && !source ? <MarkdownPreview text={content} event={target.event} commit={target.attachment ? undefined : result.commit} path={target.attachment ? undefined : target.path} attachments={target.attachment ? result.attachments : undefined} />
            : shown.length > 0 && <div className="font-mono text-xs" aria-label="Source with line numbers">{shown.map((line, i) => <div key={first + i} ref={first + i === target.line ? selected : undefined} className={`flex ${first + i === target.line ? "bg-accent/20" : ""}`}>
              <span className="mr-4 min-w-10 shrink-0 select-none text-right text-faint">{first + i}</span><pre className="whitespace-pre-wrap break-all">{line || " "}</pre></div>)}</div>}
        </>}
      </div>
    </div>
  </div>;
}
