import type { PreviewAddress } from "./address";

// References remain literal repository paths. A cited full object ID is an
// explicit selection, which the resident must also verify against the record.
export function fileReference(value: string, event: string): PreviewAddress | undefined {
  if (value.length > 2200) return undefined;
  const match = /^(.+?)(?:@([a-f0-9]{40}|[a-f0-9]{64}))?(?::([1-9]\d*)(?::\d+)?|#L([1-9]\d*)(?:-L?\d+)?)?$/.exec(value);
  if (!match) return undefined;
  const path = match[1];
  if (!path || /^(?:[a-z][a-z0-9+.-]*:|\/|#)/i.test(path) || path.includes("\\") || /[\x00-\x1f]/.test(path)) return undefined;
  if (path.split("/").some((part) => !part || part === "." || part === "..")) return undefined;
  return { event, path, commit: match[2], line: Number(match[3] || match[4]) || undefined };
}

// One place decides whether a reference a record makes in its own text opens
// that record's signed evidence attachment or a repository source file, so the
// prose, the body rows and the evidence row all agree.
//
// A token whose whole text equals the name of an attachment of this record
// opens that attachment: that is the name the record itself published, and the
// evidence row opens the same content under the same name. A token that says
// more than that bare name opens the source file, so a source path sharing an
// attachment's name stays reachable — an explicit revision (`notes.md@<full
// object ID>`), an explicit line (`notes.md:12`, `notes.md#L12`), or any other
// text that makes it differ from every attachment name, such as a directory
// component no attachment carries. Attachment names are compared as exact
// whole strings; nothing is normalised, stripped or matched by suffix.
//
// Two links never come through here and never change: the artifact `path` row
// always opens source, and the evidence list always opens the attachment.
// Nothing here guesses another record, another revision or a file outside the
// repository. With no attachment list — none fetched yet, or the listing
// failed — every token resolves exactly as it did before attachments existed.
export function referenceTarget(
  value: string,
  event: string,
  options?: { commit?: string; basePath?: string; attachments?: string[] },
): PreviewAddress | undefined {
  if (options?.attachments?.includes(value)) return { event, attachment: value };
  const reference = fileReference(value, event);
  if (!reference) return undefined;
  const basePath = options?.basePath;
  if (basePath && !reference.path?.includes("/")) reference.path = basePath.slice(0, Math.max(0, basePath.lastIndexOf("/") + 1)) + reference.path;
  return { ...reference, commit: reference.commit ?? options?.commit };
}

export function safeExternalLink(value: string): boolean {
  try { const url = new URL(value); return url.protocol === "https:" || url.protocol === "http:"; }
  catch { return false; }
}

// Only recognizable source/document paths in prose become implicit links.
// Explicit Markdown links may name extensionless files or directories too.
export const FILE_TOKEN = /(?:[A-Za-z0-9_.-]+\/)*[A-Za-z0-9_.-]+\.(?:md|mdx|json|jsonl|go|ts|tsx|js|jsx|mjs|css|html|txt|log|yaml|yml|toml|sh|py|rs|sql|csv|svg)(?:@(?:[a-f0-9]{64}|[a-f0-9]{40}))?(?::[1-9]\d*(?::\d+)?|#L[1-9]\d*(?:-L?\d+)?)?/g;

// Scan delimiters once. Unclosed markup remains text, without repeatedly
// searching the same suffix of an untrusted document.
export function inlineTokens(text: string) {
  const tokens: { start: number; end: number; code?: string; label?: string; url?: string; image?: boolean }[] = [];
  let i = 0;
  while (i < text.length) {
    if (text[i] === "`") {
      const end = text.indexOf("`", i + 1);
      if (end < 0) break;
      tokens.push({ start: i, end: end + 1, code: text.slice(i + 1, end) });
      i = end + 1;
    } else if (text[i] === "[" || text[i] === "!" && text[i + 1] === "[") {
      const image = text[i] === "!";
      const labelStart = i + (image ? 2 : 1);
      const endLabel = text.indexOf("]", labelStart);
      if (endLabel < 0) break;
      if (text[endLabel + 1] !== "(") { i = endLabel + 1; continue; }
      const end = text.indexOf(")", endLabel + 2);
      if (end < 0) break;
      tokens.push({ start: i, end: end + 1, label: text.slice(labelStart, endLabel), url: text.slice(endLabel + 2, end), image });
      i = end + 1;
    } else i++;
  }
  return tokens;
}
