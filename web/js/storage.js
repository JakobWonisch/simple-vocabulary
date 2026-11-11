const KEY = "simple-vocabulary:v1";

const defaultConjugation = () => ({
  mode: "ind", // ind | cond | sbjv | imp | ger
  tense: "prs", // ind/sbjv tense, or imp polarity: pos | neg
  order: "default", // default | random
});

const defaultGenerate = () => ({
  count: 25,
  difficulty: "easy", // easy | medium | hard
});

const defaultState = () => ({
  version: 1,
  direction: "es-de", // es-de | de-es
  conjugation: defaultConjugation(),
  generate: defaultGenerate(),
  sets: [],
});

function normalizeGenerate(raw) {
  const base = defaultGenerate();
  const count = Number(raw?.count);
  if (Number.isFinite(count)) {
    base.count = Math.min(100, Math.max(10, Math.round(count)));
  }
  const difficulty = String(raw?.difficulty || "").toLowerCase();
  if (difficulty === "easy" || difficulty === "medium" || difficulty === "hard") {
    base.difficulty = difficulty;
  }
  return base;
}

function normalizeState(parsed) {
  return {
    ...defaultState(),
    ...parsed,
    conjugation: {
      ...defaultConjugation(),
      ...(parsed.conjugation || {}),
    },
    generate: normalizeGenerate(parsed.generate),
    version: 1,
    sets: parsed.sets,
  };
}

export function hasStoredState() {
  return localStorage.getItem(KEY) !== null;
}

// loadState returns saved browser state when it exists. The seed file is
// fetched only when nothing is stored. A successful seed load is written
// immediately; an existing value is never replaced.
export async function loadState() {
  const raw = localStorage.getItem(KEY);
  if (raw !== null) {
    try {
      const parsed = JSON.parse(raw);
      if (parsed && Array.isArray(parsed.sets)) return normalizeState(parsed);
    } catch {
      // Leave the stored value untouched.
    }
    return defaultState();
  }
  try {
    const res = await fetch("/api/initial");
    if (!res.ok) return defaultState();
    const data = await res.json();
    if (!data || !Array.isArray(data.sets)) return defaultState();
    const state = normalizeState(data);
    saveState(state);
    return state;
  } catch {
    return defaultState();
  }
}

export function saveState(state) {
  localStorage.setItem(KEY, JSON.stringify(state));
}

function exportStamp(date = new Date()) {
  const pad = (n) => String(n).padStart(2, "0");
  const day = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  const time = `${pad(date.getHours())}${pad(date.getMinutes())}${pad(date.getSeconds())}`;
  return `${day}-${time}`;
}

export function exportState(state) {
  const blob = new Blob([JSON.stringify(state, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `simple-vocabulary-${exportStamp()}.json`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // Chrome reads the blob after click(). Revoking immediately leaves the
  // download interrupted, and Chrome retries it about every two minutes.
  setTimeout(() => URL.revokeObjectURL(url), 40_000);
}

export function importState(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("Datei konnte nicht gelesen werden"));
    reader.onload = () => {
      try {
        const data = JSON.parse(String(reader.result));
        if (!data || !Array.isArray(data.sets)) {
          reject(new Error("Ungültiger Vokabelexport"));
          return;
        }
        resolve(normalizeState(data));
      } catch (err) {
        reject(err);
      }
    };
    reader.readAsText(file);
  });
}

export function uid() {
  if (crypto.randomUUID) return crypto.randomUUID();
  return `id-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
