// Anonymous usage counters. Counts are batched in memory and sent as plain
// totals (e.g. {"card_studied": 12}) — no IDs, cookies, or local storage.

const FLUSH_MS = 60_000;
const pending = new Map();
let timer = 0;

export function track(name) {
  pending.set(name, (pending.get(name) || 0) + 1);
  if (!timer) timer = setTimeout(flush, FLUSH_MS);
}

function flush() {
  clearTimeout(timer);
  timer = 0;
  if (!pending.size) return;
  const body = JSON.stringify({ counts: Object.fromEntries(pending) });
  pending.clear();
  try {
    if (navigator.sendBeacon?.("/api/events", new Blob([body], { type: "application/json" }))) return;
  } catch {
    // fall through to fetch
  }
  fetch("/api/events", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
    keepalive: true,
  }).catch(() => {});
}

window.addEventListener("pagehide", flush);
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "hidden") flush();
});
