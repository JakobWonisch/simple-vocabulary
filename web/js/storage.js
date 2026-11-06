const KEY = "simple-vocabulary:v1";

const defaultConjugation = () => ({
  mode: "ind", // ind | sbjv | imp | ger
  tense: "prs",
  order: "default", // default | random
});

const defaultState = () => ({
  version: 1,
  direction: "es-de", // es-de | de-es
  conjugation: defaultConjugation(),
  sets: [],
});

function normalizeState(parsed) {
  return {
    ...defaultState(),
    ...parsed,
    conjugation: {
      ...defaultConjugation(),
      ...(parsed.conjugation || {}),
    },
    version: 1,
    sets: parsed.sets,
  };
}

export function loadState() {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return defaultState();
    const parsed = JSON.parse(raw);
    if (!parsed || !Array.isArray(parsed.sets)) return defaultState();
    return normalizeState(parsed);
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
