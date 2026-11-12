// Account API client. Session lives in an HttpOnly cookie; all calls are
// same-origin fetch. Errors carry the server message for uiMessage().

async function call(path, body) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || "Something went wrong");
    if (data.needsVerify) err.needsVerify = true;
    throw err;
  }
  return data;
}

// fetchMe resolves the current session, or null when not logged in.
export async function fetchMe() {
  try {
    const res = await fetch("/api/auth/me");
    if (!res.ok) return null;
    const data = await res.json().catch(() => null);
    return data?.email ? { email: data.email } : null;
  } catch {
    return null;
  }
}

export async function fetchAccountState() {
  const res = await fetch("/api/account/state");
  if (!res.ok) throw new Error("could not load account state");
  return res.json();
}

export async function signup(email, password, copyLocal, state) {
  return call("/api/auth/signup", { email, password, copyLocal, state });
}

export async function verifyEmail(email, code) {
  return call("/api/auth/verify", { email, code });
}

export async function resendCode(email) {
  return call("/api/auth/resend", { email });
}

export async function login(email, password) {
  return call("/api/auth/login", { email, password });
}

export async function logout() {
  return call("/api/auth/logout", {});
}

// saveAccountState PUTs the full state blob. keepalive lets the call finish
// during pagehide. Callers debounce; this is one shot.
export function saveAccountState(state) {
  return fetch("/api/account/state", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(state),
    keepalive: true,
  });
}

// beaconAccountState flushes the state via sendBeacon on pagehide; the server
// accepts POST on the same route for this.
export function beaconAccountState(state) {
  try {
    return navigator.sendBeacon(
      "/api/account/state",
      new Blob([JSON.stringify(state)], { type: "application/json" }),
    );
  } catch {
    return false;
  }
}
