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
