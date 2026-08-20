type Announce = (actor: string, credential: string) => Promise<{ credential?: string }>;

// renewCredential forgets only a credential the resident has explicitly
// rejected. The next heartbeat then calls announce with no credential and the
// resident mints a new lease. Transport failures keep the current credential
// so a transient outage does not create needless identities.
export async function renewCredential(actor: string, credential: string, announce: Announce): Promise<string> {
  try {
    const response = await announce(actor, credential);
    return credential || response.credential || "";
  } catch (error) {
    if (credential && error instanceof Error && error.message === "credential is not valid") return "";
    throw error;
  }
}
