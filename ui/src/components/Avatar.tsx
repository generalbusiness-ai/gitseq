import { hueOf, initialsOf } from "../lib/avatar";
import { cn } from "../lib/util";

// A deterministic per-actor face: a small SVG disc whose hue is derived from
// the actor's key fingerprint, carrying one or two initials. The same
// identity looks the same everywhere — chat gutter, act rows, presence,
// profiles — with no image storage anywhere.

export function Avatar({
  fingerprint,
  name,
  title,
  size = 36,
  onClick,
  className,
}: {
  fingerprint: string;
  name: string;
  title?: string; // hover and label text when there is more to say than the name
  size?: number;
  onClick?: () => void; // when set, the avatar is a button that opens the profile
  className?: string;
}) {
  const hue = hueOf(fingerprint || name);
  const initials = initialsOf(name);
  const label = title ?? name;
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
      <span role="img" aria-label={label} title={label} className={cn("inline-block shrink-0 rounded-full", className)}>
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
      aria-label={label}
      title={label}
      className={cn(
        "inline-block shrink-0 rounded-full transition-transform hover:scale-105 focus-visible:outline focus-visible:outline-accent",
        className,
      )}
    >
      {disc}
    </button>
  );
}
