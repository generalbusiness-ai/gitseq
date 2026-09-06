import { POPULATIONS, type Population } from "./rows";

export interface PreviewAddress {
  event: string;
  path?: string;
  commit?: string;
  attachment?: string;
  line?: number;
}
export type Address = ({ kind: "list"; population?: Population } | { kind: "notes" } | { kind: "thread"; event: string; focus?: string }) & {
  preview?: PreviewAddress;
  error?: string;
};
const POPULATION_KEYS = new Set<string>(POPULATIONS.map((tab) => tab.key));

// Decode each path segment once, after splitting. A canonical event carries
// its own '#'; old raw links and copied percent-encoded links name the same
// record. A damaged focus must not discard an otherwise valid record.
export function parseAddress(hash: string): Address {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash;
  const separator = raw.indexOf("?");
  const route = separator < 0 ? raw : raw.slice(0, separator);
  const query = separator < 0 ? "" : raw.slice(separator + 1);
  const parts = route.split("/").filter(Boolean);
  let error: string | undefined;
  const decode = (value: string | undefined) => {
    if (value === undefined) return undefined;
    try { return decodeURIComponent(value); }
    catch { error = "This link contains malformed encoding."; return value; }
  };
  let address: Address;
  if (parts[0] === "thread" && parts[1]) {
    const event = decode(parts[1])!;
    const focus = decode(parts[2]);
    address = { kind: "thread", event, focus };
  } else if (parts[0] === "notes") address = { kind: "notes" };
  else if (parts[0] === "thread") address = { kind: "list" };
  else address = { kind: "list", population: parts[0] === "list" && POPULATION_KEYS.has(parts[1]) ? parts[1] as Population : undefined };
  try {
    decodeURIComponent(query.replace(/\+/g, " "));
    const params = new URLSearchParams(query);
    const event = params.get("preview_event");
    if (event) {
      const line = params.get("line");
      address.preview = { event, path: params.get("file") || undefined, commit: params.get("at") || undefined,
        attachment: params.get("evidence") || undefined, line: line && /^[1-9]\d{0,6}$/.test(line) ? Number(line) : undefined };
    }
  } catch { error = "The preview link contains malformed encoding."; }
  if (error) address.error = error;
  return address;
}

export function formatAddress(address: Address): string {
  const segment = (value: string) => /[?/%]/.test(value) ? encodeURIComponent(value) : value;
  let route = address.kind === "thread" ? (address.focus ? `#/thread/${segment(address.event)}/${segment(address.focus)}` : `#/thread/${segment(address.event)}`)
    : address.kind === "notes" ? "#/notes" : address.population ? `#/list/${address.population}` : "#/list";
  if (address.preview) {
    const p = address.preview;
    const params = new URLSearchParams({ preview_event: p.event });
    if (p.path) params.set("file", p.path);
    if (p.commit) params.set("at", p.commit);
    if (p.attachment) params.set("evidence", p.attachment);
    if (p.line) params.set("line", String(p.line));
    route += `?${params}`;
  }
  return route;
}
