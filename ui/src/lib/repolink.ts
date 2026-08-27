// The one place a remote URL is allowed to become an href.
//
// The service already refuses anything it will not link, but this is the site
// where a string turns into an attribute the browser will navigate, so it
// decides for itself rather than trusting the field it was handed: an older
// resident, a stale embed, or any future caller gets the same answer here.
//
// The rule is an allowlist. The URL is parsed, and only the http and https
// protocols are admitted; every other scheme — ssh, file, javascript, data, and
// any scheme nobody has thought of — falls through to undefined and renders as
// plain text. Nothing is compared against a list of dangerous schemes, so being
// unrecognised is itself a refusal rather than a way past the check.
//
// Userinfo declines the link outright rather than being stripped, because a
// remote of the form https://x-access-token:SECRET@host/org/repo carries a
// credential: refusing keeps it out of the DOM entirely, where stripping would
// leave every later rendering path responsible for dropping it again.
//
// The returned string is the parser's own serialization, not the caller's
// input, so what reaches the attribute is what the parser actually understood.
export function repoRemoteHref(remote: string | undefined | null): string | undefined {
  if (!remote) return undefined;
  let parsed: URL;
  try {
    parsed = new URL(remote);
  } catch {
    return undefined;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
  if (parsed.username !== "" || parsed.password !== "") return undefined;
  return parsed.href;
}
