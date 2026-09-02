import { POPULATIONS, type Population } from "./rows";

// The address names the screen the way a reader would say it out loud: a
// list with its selected tab, or a thread carrying the record it opened.
// Event ids ride in the address exactly as the fold writes them, full and
// canonical, never abbreviated and never reassembled from fragments, because
// an address is exactly the place a truncated id would recur silently.
export type Address = { kind: "list"; population?: Population } | { kind: "thread"; event: string; focus?: string };

const POPULATION_KEYS = new Set<string>(POPULATIONS.map((tab) => tab.key));

// The fragment after the first '#' is ours to read; a canonical event id
// carries its own '#', which stays part of the data. Ids never contain '/',
// so the path splits cleanly on it. Anything unrecognised is the default
// list, so a stale or mistyped address degrades to the board, never to a
// blank screen.
export function parseAddress(hash: string): Address {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash;
  const parts = raw.split("/").filter((part) => part.length > 0);
  if (parts[0] !== "thread") {
    const population = parts[0] === "list" && parts[1] && POPULATION_KEYS.has(parts[1]) ? (parts[1] as Population) : undefined;
    return { kind: "list", population };
  }
  if (!parts[1]) return { kind: "list" };
  return { kind: "thread", event: parts[1], focus: parts[2] };
}

export function formatAddress(address: Address): string {
  if (address.kind === "thread") {
    return address.focus ? `#/thread/${address.event}/${address.focus}` : `#/thread/${address.event}`;
  }
  return address.population ? `#/list/${address.population}` : "#/list";
}
