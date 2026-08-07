import { cn } from "../lib/util";

// A deterministic per-actor face: a small SVG disc whose hue is derived from
// the actor's key fingerprint, carrying one or two initials. The same
// identity looks the same everywhere — chat gutter, act rows, presence,
// profiles — with no image storage anywhere.

function hueOf(seed: string): number {
  // FNV-1a over the fingerprint keeps the hue stable across sessions.
  let hash = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    hash ^= seed.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0) % 360;
}

function initialsOf(name: string): string {
  const parts = name.split(/[^A-Za-z0-9]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return (parts[0] ?? name ?? "?").slice(0, 2).toUpperCase();
}

export function Avatar({
  fingerprint,
  name,
  size = 36,
  onClick,
  className,
}: {
  fingerprint: string;
  name: string;
  size?: number;
  onClick?: () => void; // when set, the avatar is a button that opens the profile
  className?: string;
}) {
  const hue = hueOf(fingerprint || name);
  const initials = initialsOf(name);
  const disc = (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden className="block">
      <circle cx="16" cy="16" r="16" fill={`oklch(0.55 0.11 ${hue})`} />
      <text
        x="16"
        y="16"
        dy="0.36em"
        textAnchor="middle"
        fontSize={initials.length > 1 ? 12.5 : 14.5}
        fontWeight={600}
        fill="oklch(0.97 0.01 286)"
        style={{ fontFamily: "var(--font-sans)", letterSpacing: "0.02em" }}
      >
        {initials}
      </text>
    </svg>
  );
  if (!onClick) {
    return (
      <span role="img" aria-label={name} title={name} className={cn("inline-block shrink-0 rounded-full", className)}>
        {disc}
      </span>
    );
  }
  return (
    <button
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      aria-label={name}
      title={name}
      className={cn(
        "inline-block shrink-0 rounded-full transition-transform hover:scale-105 focus-visible:outline focus-visible:outline-accent",
        className,
      )}
    >
      {disc}
    </button>
  );
}
