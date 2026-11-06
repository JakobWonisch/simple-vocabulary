const KEY = "simple-vocabulary:v1";

const defaultState = () => ({
  version: 1,
  direction: "es-de", // es-de | de-es
  sets: [],
});

export function loadState() {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return defaultState();
    const parsed = JSON.parse(raw);
    if (!parsed || !Array.isArray(parsed.sets)) return defaultState();
    return {
      ...defaultState(),
      ...parsed,
      version: 1,
    };
  } catch {
    return defaultState();
  }
}

export function saveState(state) {
  localStorage.setItem(KEY, JSON.stringify(state));
}

export function exportState(state) {
  const blob = new Blob([JSON.stringify(state, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  const stamp = new Date().toISOString().slice(0, 10);
  a.href = url;
  a.download = `simple-vocabulary-${stamp}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

export function importState(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("Could not read file"));
    reader.onload = () => {
      try {
        const data = JSON.parse(String(reader.result));
        if (!data || !Array.isArray(data.sets)) {
          reject(new Error("Invalid vocabulary export"));
          return;
        }
        resolve({
          ...defaultState(),
          ...data,
          version: 1,
        });
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
