import { timesSeen } from "./memory.js";

// Four views per word fill the bar. Later views still raise the percentage.
export const MASTERY_LEVEL = 4;

export function masteryRatio(cards) {
  const list = Array.isArray(cards) ? cards : [];
  const total = list.length * MASTERY_LEVEL;
  if (total <= 0) return 0;
  let steps = 0;
  for (const card of list) steps += Math.max(0, timesSeen(card));
  return steps / total;
}

// Drop the single fastest and single slowest sample once three exist.
// Fewer samples stay as a plain average, because trimming would erase them.
export function trimmedMean(samples) {
  const nums = [];
  for (const value of samples || []) {
    const n = Number(value);
    if (Number.isFinite(n) && n >= 0) nums.push(n);
  }
  if (!nums.length) return null;
  nums.sort((a, b) => a - b);
  const body = nums.length >= 3 ? nums.slice(1, -1) : nums;
  return body.reduce((sum, n) => sum + n, 0) / body.length;
}

export function recordSample(card, ms) {
  const value = Number(ms);
  if (!card || !Number.isFinite(value) || value < 0) return false;
  if (!Array.isArray(card.times)) card.times = [];
  card.times.push(Math.round(value));
  return true;
}

export function formatPercent(ratio) {
  const value = Number.isFinite(ratio) ? Math.max(0, ratio) : 0;
  return value.toLocaleString("de-DE", { style: "percent", maximumFractionDigits: 1 });
}

export function formatDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const seconds = ms / 1000;
  if (seconds < 60) {
    const digits = seconds < 10 ? 1 : 0;
    return `${seconds.toLocaleString("de-DE", {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    })} s`;
  }
  const total = Math.round(seconds);
  const minutes = Math.floor(total / 60);
  const rest = total % 60;
  return `${minutes}:${String(rest).padStart(2, "0")} min`;
}

// First click uses `first`. The next click flips it. A third click clears the sort.
export function nextStatsSort(current, key, first) {
  if (current?.key !== key) return { key, dir: first };
  if (current.dir === first) return { key, dir: first === "asc" ? "desc" : "asc" };
  return { key: null, dir: null };
}

export function sortStatsRows(rows, sort) {
  const list = rows.slice();
  if (!sort?.key) return list.sort((a, b) => a.index - b.index);
  const sign = sort.dir === "desc" ? -1 : 1;
  return list.sort((a, b) => {
    let diff = 0;
    if (sort.key === "word") {
      diff = String(a.word).localeCompare(String(b.word), "es", { sensitivity: "base" });
    } else if (sort.key === "seen") {
      diff = a.seen - b.seen;
    } else if (sort.key === "time") {
      const aOk = Number.isFinite(a.time);
      const bOk = Number.isFinite(b.time);
      if (aOk && bOk) diff = a.time - b.time;
      else if (aOk !== bOk) return aOk ? -1 : 1;
    }
    if (diff === 0) return a.index - b.index;
    return sign * diff;
  });
}
