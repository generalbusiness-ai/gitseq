// The two derivations behind an actor's face. They live apart from the
// component so they can be pinned by test: the initials come from the actor's
// name and nothing else, so decorated labels — "claude — 3 sessions" — belong
// in the avatar's title, never in its name, where they would silently rewrite
// the initials to "C3".

// FNV-1a over the fingerprint keeps the hue stable across sessions.
export function hueOf(seed: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    hash ^= seed.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0) % 360;
}

export function initialsOf(name: string): string {
  const parts = name.split(/[^A-Za-z0-9]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return (parts[0] ?? name ?? "?").slice(0, 2).toUpperCase();
}
