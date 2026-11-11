import { loadState, saveState, exportState, importState, uid, hasStoredState } from "./storage.js";
import { pickCard, markSeen, timesSeen, formatWithArticle } from "./memory.js";
import { MASTERY_LEVEL, formatDuration, formatPercent, masteryRatio, nextStatsSort, recordSample, sortStatsRows, trimmedMean } from "./stats.js";

const app = document.getElementById("app");
const settingsBtn = document.getElementById("settings-btn");
const settingsPop = document.getElementById("settings-pop");
const infoBtn = document.getElementById("info-btn");
const infoPop = document.getElementById("info-pop");
const directionInputs = settingsPop.querySelectorAll('input[name="direction"]');
const themeInputs = settingsPop.querySelectorAll('input[name="theme"]');
const themeColorMeta = document.querySelector('meta[name="theme-color"]');
const addWordDialog = document.getElementById("add-word-dialog");
const addWordInput = document.getElementById("add-word-input");
const addWordResults = document.getElementById("add-word-results");
const addWordStatus = document.getElementById("add-word-status");
const addWordTitle = document.getElementById("add-word-title");
const addWordLabel = document.getElementById("add-word-label");
const btnAddWord = document.getElementById("btn-add-word");

const knownErrors = {
  "Generation failed": "Erzeugung fehlgeschlagen",
  "No cards returned — try a broader topic": "Keine Karten erhalten — versuche ein breiteres Thema",
  "Timed out waiting for the AI. Try again in a minute, or set OPENROUTER_MODEL to a faster/paid model.":
    "Zeitüberschreitung bei der KI. Versuche es in einer Minute erneut oder setze OPENROUTER_MODEL auf ein schnelleres oder kostenpflichtiges Modell.",
  "Something went wrong": "Etwas ist schiefgelaufen",
  "Import failed": "Import fehlgeschlagen",
  "AI provider not configured (set OPENROUTER_API_KEY)":
    "KI-Anbieter ist nicht eingerichtet (OPENROUTER_API_KEY setzen)",
  "invalid JSON body": "Ungültiger JSON-Inhalt",
  "topic is required": "Thema ist erforderlich",
  "vocabulary generation failed": "Vokabeln konnten nicht erzeugt werden",
  "Not enough of the suggested words are in the dictionary. Try a broader topic.":
    "Zu wenige der vorgeschlagenen Wörter stehen im Wörterbuch. Versuche ein breiteres Thema.",
  "Not enough of the suggested verbs are in the dictionary. Try a broader topic.":
    "Zu wenige der vorgeschlagenen Verben stehen als Verb im Wörterbuch. Versuche ein breiteres Thema.",
  "conjugation data is not loaded": "Konjugationsdaten sind nicht geladen",
  "kind must be vocab or conjugation": "Die Sammlungsart ist ungültig",
  "difficulty must be easy, medium, or hard": "Die Schwierigkeit ist ungültig",
  "AI providers are rate-limited right now. Wait a minute and retry, or set OPENROUTER_MODEL to another model.":
    "Die KI-Anbieter sind gerade ausgelastet. Warte eine Minute und versuche es erneut, oder setze OPENROUTER_MODEL auf ein anderes Modell.",
  "AI rate limit hit. Retry shortly, or change OPENROUTER_MODEL.":
    "KI-Limit erreicht. Versuche es gleich erneut oder ändere OPENROUTER_MODEL.",
  "sharing is not configured": "Teilen ist auf dem Server nicht eingerichtet",
  "shared set not found": "Geteilte Sammlung nicht gefunden",
  "could not store shared set": "Sammlung konnte nicht veröffentlicht werden",
  "could not load shared set": "Geteilte Sammlung konnte nicht geladen werden",
  "invalid shared set": "Ungültige geteilte Sammlung",
};

function meaningList(card) {
  const raw = Array.isArray(card?.translations) ? card.translations : [];
  const list = [];
  const seen = new Set();
  for (const item of raw) {
    const text = String(item || "").trim();
    const key = text.toLocaleLowerCase();
    if (!text || seen.has(key)) continue;
    seen.add(key);
    list.push(text);
    if (list.length === 3) break;
  }
  if (!list.length && card?.target) list.push(card.target);
  return list;
}

async function refreshConjugationParadigms(set) {
  const lemmas = [];
  const seen = new Set();
  for (const card of set.cards || []) {
    const lemma = String(card.source || "").trim();
    const key = lemma.toLocaleLowerCase();
    if (!lemma || seen.has(key)) continue;
    seen.add(key);
    lemmas.push(lemma);
  }
  if (!lemmas.length) return;
  try {
    const res = await fetch("/api/conj/paradigms", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ lemmas }),
    });
    if (!res.ok) return;
    const data = await res.json();
    const paradigms = data.paradigms || {};
    let changed = false;
    for (const card of set.cards || []) {
      const paradigm = paradigms[card.source];
      if (!paradigm) continue;
      card.paradigm = paradigm;
      changed = true;
    }
    if (changed) persist();
  } catch {
    // Study stays usable if the server is offline or has not been restarted.
  }
}

async function ensureTranslations(card) {
  if (card.glossChecked || meaningList(card).length >= 3) return;
  try {
    const res = await fetch(`/api/dict/lookup?q=${encodeURIComponent(card.source || "")}`);
    if (!res.ok) {
      if (!meaningList(card).length) card.translations = card.target ? [card.target] : [];
      card.glossChecked = true;
      persist();
      return;
    }
    const entry = await res.json();
    const list = meaningList({ translations: entry.translations, target: card.target });
    card.translations = list.length ? list : card.target ? [card.target] : [];
    card.glossChecked = true;
    persist();
  } catch {
    // Study stays usable offline; try the lookup again next time.
  }
}

function uiMessage(message, fallback) {
  if (message && knownErrors[message]) return knownErrors[message];
  if (message) return message;
  return knownErrors[fallback] || fallback;
}

let state = {
  version: 1,
  theme: null,
  direction: "es-de",
  conjugation: { mode: "ind", tense: "prs", order: "default" },
  generate: { count: 25, difficulty: "easy" },
  sets: [],
};
let stateReady = false;
let unbindStudyKeys = () => {};
let refreshConjugation = () => {};
let resetConjugationOrder = () => {};
let repaintStudy = () => {};
let closeOpenSetMenu = () => {};
let openMenuButton = null;

const PERSONS = [
  { id: "yo", index: 0, label: "yo" },
  { id: "tu", index: 1, label: "tú" },
  { id: "el", index: 2, label: "él/ella/usted" },
  { id: "nos", index: 3, label: "nosotros" },
  { id: "vos", index: 4, label: "vosotros" },
  { id: "ellos", index: 5, label: "ellos/ellas/ustedes" },
];

const TENSE_OPTIONS = {
  ind: [
    ["prs", "Presente"],
    ["pfv", "Pretérito indefinido"],
    ["ipfv", "Imperfecto"],
    ["fut", "Futuro"],
  ],
  sbjv: [
    ["prs", "Presente"],
    ["ra", "Imperfecto (-ra)"],
    ["se", "Imperfecto (-se)"],
    ["fut", "Futuro"],
  ],
  imp: [
    ["pos", "Afirmativo/Bejahend"],
    ["neg", "Negativo/Verneinend"],
  ],
};

const MODE_LABEL = {
  ind: "Indicativo/Indikativ",
  cond: "Condicional/Konditional",
  sbjv: "Subjuntivo/Subjunktiv",
  imp: "Imperativo/Imperativ",
  ger: "Gerundio y participio/Gerundium & Partizip",
};

function persist() {
  if (!stateReady) return;
  saveState(state);
}

function parseRoute() {
  const raw = location.hash.replace(/^#/, "") || "/";
  const [path, search = ""] = raw.split("?");
  return {
    parts: path.split("/").filter(Boolean),
    query: new URLSearchParams(search),
  };
}

const difficultyLabels = {
  easy: "Einfach",
  medium: "Mittel",
  hard: "Schwierig",
};

function selectedDifficulty(value) {
  return difficultyLabels[value] ? value : "easy";
}

function difficultyLabel(value) {
  return difficultyLabels[value] || "";
}

function clampGenerateCount(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 25;
  return Math.min(100, Math.max(10, Math.round(n)));
}

function setDifficultyPick(form, difficulty) {
  const level = selectedDifficulty(difficulty);
  const pick = form.querySelector(".pick");
  const value = pick.querySelector(".pick-value");
  const hidden = pick.querySelector('input[name="difficulty"]');
  const options = [...pick.querySelectorAll(".pick-option")];
  const match = options.find((opt) => opt.dataset.value === level) || options[0];
  for (const item of options) item.setAttribute("aria-selected", item === match ? "true" : "false");
  hidden.value = match.dataset.value;
  value.textContent = match.textContent.trim();
}

let difficultyPickAbort = null;
let unbindShareDock = () => {};

function bindDifficultyPick(form) {
  difficultyPickAbort?.abort();
  difficultyPickAbort = new AbortController();
  const { signal } = difficultyPickAbort;
  const pick = form.querySelector(".pick");
  const toggle = pick.querySelector(".pick-toggle");
  const menu = pick.querySelector(".pick-menu");
  const value = pick.querySelector(".pick-value");
  const hidden = pick.querySelector('input[name="difficulty"]');
  const options = [...menu.querySelectorAll(".pick-option")];
  const selected = () => options.find((opt) => opt.getAttribute("aria-selected") === "true") || options[0];

  const close = (focusToggle = false) => {
    menu.hidden = true;
    toggle.setAttribute("aria-expanded", "false");
    if (focusToggle) toggle.focus();
  };

  const open = () => {
    menu.hidden = false;
    toggle.setAttribute("aria-expanded", "true");
    selected().focus();
  };

  const choose = (opt) => {
    for (const item of options) item.setAttribute("aria-selected", item === opt ? "true" : "false");
    hidden.value = opt.dataset.value;
    value.textContent = opt.textContent.trim();
    close(true);
  };

  toggle.addEventListener("click", () => {
    if (menu.hidden) open();
    else close();
  }, { signal });

  toggle.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      if (menu.hidden) open();
    } else if (e.key === "Escape") {
      close();
    }
  }, { signal });

  menu.addEventListener("keydown", (e) => {
    const index = options.indexOf(document.activeElement);
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const delta = e.key === "ArrowDown" ? 1 : -1;
      const next = options[(index + delta + options.length) % options.length];
      next.focus();
    } else if (e.key === "Home") {
      e.preventDefault();
      options[0].focus();
    } else if (e.key === "End") {
      e.preventDefault();
      options.at(-1).focus();
    } else if (e.key === "Escape") {
      e.preventDefault();
      close(true);
    } else if (e.key === "Tab") {
      close();
    }
  }, { signal });

  for (const opt of options) {
    opt.addEventListener("click", () => choose(opt), { signal });
  }

  document.addEventListener("pointerdown", (e) => {
    if (!pick.contains(e.target)) close();
  }, { signal });
}

function homeHash(kind) {
  return kind === "conjugation" ? "#/conjugation" : "#/";
}

function route() {
  if (!stateReady) return;
  difficultyPickAbort?.abort();
  difficultyPickAbort = null;
  closeOpenSetMenu();
  unbindShareDock();
  unbindShareDock = () => {};
  if (!addWordDialog.hidden) setAddWordOpen(false);
  unbindStudyKeys();
  unbindStudyKeys = () => {};
  refreshConjugation = () => {};
  resetConjugationOrder = () => {};
  repaintStudy = () => {};
  const { parts, query } = parseRoute();

  if (parts[0] === "new") {
    renderCreate(query.get("kind"));
  } else if (parts[0] === "study" && parts[1]) {
    const set = state.sets.find((s) => s.id === parts[1]);
    if (set?.kind === "conjugation") renderConjugation(parts[1]);
    else renderStudy(parts[1]);
  } else if (parts[0] === "stats" && parts[1]) {
    renderStats(parts[1]);
  } else if (parts[0] === "share" && parts[1]) {
    renderShare(parts[1]);
  } else {
    renderHome();
  }
  syncSettingsScope();
}

function cloneTemplate(id) {
  const tpl = document.getElementById(id);
  return tpl.content.cloneNode(true);
}

function renderHome() {
  app.replaceChildren(cloneTemplate("tpl-home"));

  const { parts } = parseRoute();
  const tab = parts[0] === "conjugation" ? "conjugation" : "vocab";
  const tabButtons = {
    vocab: document.getElementById("tab-vocab"),
    conjugation: document.getElementById("tab-conj"),
  };
  const panels = {
    vocab: document.getElementById("panel-vocab"),
    conjugation: document.getElementById("panel-conj"),
  };

  for (const kind of ["vocab", "conjugation"]) {
    const on = kind === tab;
    tabButtons[kind].setAttribute("aria-selected", on ? "true" : "false");
    tabButtons[kind].tabIndex = on ? 0 : -1;
    panels[kind].hidden = !on;
  }

  tabButtons.vocab.addEventListener("click", () => {
    if (tab !== "vocab") location.hash = "#/";
  });
  tabButtons.conjugation.addEventListener("click", () => {
    if (tab !== "conjugation") location.hash = "#/conjugation";
  });

  const sets = [...state.sets].sort((a, b) => (b.updatedAt || "").localeCompare(a.updatedAt || ""));
  fillSetList(document.getElementById("set-list-vocab"), document.getElementById("empty-vocab"), sets.filter((set) => set.kind !== "conjugation"), "vocab");
  fillSetList(document.getElementById("set-list-conj"), document.getElementById("empty-conj"), sets.filter((set) => set.kind === "conjugation"), "conjugation");
}

function masteryEl() {
  const el = document.createElement("div");
  el.className = "mastery";
  el.setAttribute("role", "progressbar");
  el.setAttribute("aria-valuemin", "0");
  el.setAttribute("aria-valuemax", "100");
  el.setAttribute("aria-valuenow", "0");
  el.setAttribute("aria-label", `Fortschritt, 100 % sind ${MASTERY_LEVEL} Wiederholungen pro Wort`);
  el.innerHTML = `<span class="mastery-track"><span class="mastery-fill"></span></span><span class="mastery-pct"></span>`;
  return el;
}

function paintMastery(el, cards) {
  if (!el) return;
  const ratio = masteryRatio(cards);
  const label = formatPercent(ratio);
  const filled = Math.min(1, Math.max(0, ratio));
  el.setAttribute("aria-valuenow", String(Math.round(filled * 100)));
  el.setAttribute("aria-valuetext", label);
  el.setAttribute("aria-label", `Fortschritt, 100 % sind ${MASTERY_LEVEL} Wiederholungen pro Wort`);
  el.title = `100 % sind ${MASTERY_LEVEL} Mal pro Wort. Weitere Wiederholungen zählen im Prozentwert weiter.`;
  const fill = el.querySelector(".mastery-fill");
  if (fill) fill.style.width = `${filled * 100}%`;
  const pct = el.querySelector(".mastery-pct");
  if (pct) pct.textContent = label;
}

function placeSetMenu(pop, anchor) {
  const rect = anchor.getBoundingClientRect();
  const width = pop.offsetWidth;
  const height = pop.offsetHeight;
  let top = rect.bottom + 6;
  if (top + height > window.innerHeight - 8) top = Math.max(8, rect.top - height - 6);
  let left = rect.right - width;
  left = Math.max(8, Math.min(left, window.innerWidth - width - 8));
  pop.style.top = `${top}px`;
  pop.style.left = `${left}px`;
}

function openSetMenu(pop, toggle) {
  closeOpenSetMenu();
  const menu = toggle.closest(".set-menu");
  menu?.classList.add("is-open");
  pop.hidden = false;
  toggle.setAttribute("aria-expanded", "true");
  placeSetMenu(pop, toggle);
  const scroller = toggle.closest(".set-scroll");
  const onScroll = () => closeOpenSetMenu();
  scroller?.addEventListener("scroll", onScroll);
  const onResize = () => closeOpenSetMenu();
  window.addEventListener("resize", onResize);
  openMenuButton = toggle;
  pop.querySelector("button")?.focus();
  closeOpenSetMenu = () => {
    pop.hidden = true;
    toggle.setAttribute("aria-expanded", "false");
    menu?.classList.remove("is-open");
    scroller?.removeEventListener("scroll", onScroll);
    window.removeEventListener("resize", onResize);
    openMenuButton = null;
    closeOpenSetMenu = () => {};
  };
}

function menuButton(label, className, action) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = className;
  button.setAttribute("role", "menuitem");
  button.textContent = label;
  button.addEventListener("click", (e) => {
    e.stopPropagation();
    closeOpenSetMenu();
    action();
  });
  return button;
}

function fillSetList(list, empty, sets, kind) {
  empty.hidden = sets.length > 0;
  for (const set of sets) {
    const li = document.createElement("li");
    const count = set.cards?.length ?? 0;
    const seen = (set.cards || []).filter((card) => timesSeen(card) > 0).length;
    const kindLabel = kind === "conjugation" ? `${count} Verben` : `${count} Wörter`;
    const level = difficultyLabel(set.difficulty);
    const meta = level ? `${kindLabel} · ${level} · ${seen} gesehen` : `${kindLabel} · ${seen} gesehen`;
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "set-card";
    const title = document.createElement("strong");
    title.textContent = set.title;
    const bar = masteryEl();
    paintMastery(bar, set.cards);
    const metaEl = document.createElement("span");
    metaEl.className = "set-meta";
    metaEl.textContent = meta;
    btn.append(title, bar, metaEl);
    btn.addEventListener("click", () => {
      location.hash = `#/study/${set.id}`;
    });

    const menu = document.createElement("div");
    menu.className = "set-menu";
    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "set-menu-btn";
    toggle.setAttribute("aria-label", `Menü für ${set.title}`);
    toggle.setAttribute("aria-haspopup", "menu");
    toggle.setAttribute("aria-expanded", "false");
    toggle.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true" fill="currentColor"><circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg>`;
    const pop = document.createElement("div");
    pop.className = "set-menu-pop";
    pop.setAttribute("role", "menu");
    pop.hidden = true;
    pop.append(
      menuButton("Statistik", "", () => {
        location.hash = `#/stats/${set.id}`;
      }),
      menuButton("Teilen", "", () => {
        shareSet(set);
      }),
      menuButton("Löschen", "danger", () => {
        if (!confirm(`Sammlung „${set.title}“ löschen?`)) return;
        state.sets = state.sets.filter((s) => s.id !== set.id);
        persist();
        renderHome();
      }),
    );
    pop.addEventListener("click", (e) => e.stopPropagation());
    toggle.addEventListener("click", (e) => {
      e.stopPropagation();
      if (!pop.hidden) closeOpenSetMenu();
      else openSetMenu(pop, toggle);
    });
    menu.append(toggle, pop);
    li.append(btn, menu);
    list.appendChild(li);
  }
}

function statsNote() {
  return "Die Zeit läuft, bis du bei Vokabeln die Antwort aufdeckst und bei der Konjugation alle Formen. Der Durchschnitt lässt die schnellste und die langsamste Messung aus, sobald mindestens drei Zeiten vorliegen.";
}

function timeTitle(samples) {
  const n = Array.isArray(samples) ? samples.length : 0;
  if (!n) return "Noch keine Messung";
  if (n < 3) return n === 1 ? "1 Messung" : `${n} Messungen`;
  return `${n} Messungen, schnellste und langsamste ausgelassen`;
}

function renderStats(setId) {
  const set = state.sets.find((s) => s.id === setId);
  if (!set) {
    location.hash = "#/";
    return;
  }
  app.replaceChildren(cloneTemplate("tpl-stats"));
  const back = document.getElementById("stats-back");
  back.href = homeHash(set.kind);
  document.getElementById("stats-title").textContent = set.title;
  document.getElementById("stats-note").textContent = statsNote();

  const cards = set.cards || [];
  const body = document.getElementById("stats-body");
  const empty = document.getElementById("stats-empty");
  const table = body.closest("table");
  empty.hidden = cards.length > 0;
  if (table) table.hidden = cards.length === 0;
  const conj = set.kind === "conjugation";
  const rows = cards.map((card, index) => {
    const samples = Array.isArray(card.times) ? card.times : [];
    return {
      index,
      card,
      word: conj ? card.source || "" : formatWithArticle(card.source, card.sourceArticle),
      seen: timesSeen(card),
      time: trimmedMean(samples),
      samples,
    };
  });
  let sort = { key: null, dir: null };

  function renderRow(entry) {
    const row = document.createElement("tr");
    const word = document.createElement("td");
    const name = document.createElement("span");
    name.className = "stats-word";
    name.textContent = entry.word;
    word.append(name);
    const gloss = meaningList(entry.card)[0];
    if (gloss) {
      const sub = document.createElement("span");
      sub.className = "stats-gloss";
      sub.textContent = gloss;
      word.append(sub);
    }
    const views = document.createElement("td");
    views.className = "num";
    views.textContent = String(entry.seen);
    const time = document.createElement("td");
    time.className = "num";
    time.textContent = formatDuration(entry.time);
    time.title = timeTitle(entry.samples);
    row.append(word, views, time);
    return row;
  }

  function paintRows() {
    body.replaceChildren(...sortStatsRows(rows, sort).map(renderRow));
    if (!table) return;
    for (const button of table.querySelectorAll(".stats-sort")) {
      const th = button.closest("th");
      const active = sort.key === button.dataset.sort;
      th.setAttribute("aria-sort", active ? (sort.dir === "asc" ? "ascending" : "descending") : "none");
    }
  }

  table?.addEventListener("click", (e) => {
    const button = e.target.closest(".stats-sort");
    if (!button) return;
    const first = button.dataset.first === "desc" ? "desc" : "asc";
    sort = nextStatsSort(sort, button.dataset.sort, first);
    paintRows();
  });
  paintRows();
}

function renderCreate(kindHint) {
  app.replaceChildren(cloneTemplate("tpl-new"));
  const form = document.getElementById("create-form");
  const status = document.getElementById("create-status");
  const submit = document.getElementById("create-submit");
  const back = document.querySelector(".view.create .back");
  const prefs = state.generate || { count: 25, difficulty: "easy" };
  const countInput = form.querySelector('input[name="count"]');
  countInput.value = String(clampGenerateCount(prefs.count));
  setDifficultyPick(form, prefs.difficulty);

  const lede = document.getElementById("create-lede");
  const countLabel = document.getElementById("create-count-label");
  const selectedKind = () =>
    form.querySelector('input[name="kind"]:checked')?.value === "conjugation" ? "conjugation" : "vocab";
  if (kindHint === "conjugation" || kindHint === "vocab") {
    const radio = form.querySelector(`input[name="kind"][value="${kindHint}"]`);
    if (radio) radio.checked = true;
  }
  const syncKindCopy = () => {
    const conj = selectedKind() === "conjugation";
    const count = clampGenerateCount(countInput.value);
    lede.textContent = conj
      ? "Beschreibe ein Thema. Wir erzeugen spanische Verben dazu und behalten nur die, die im Wörterbuch als Verb stehen."
      : `Beschreibe ein Thema. Wir erzeugen etwa ${count} spanische Wörter und schlagen die deutschen Übersetzungen im eingebauten Wörterbuch nach.`;
    countLabel.textContent = conj ? "Verben" : "Wortanzahl";
    if (back) back.href = homeHash(conj ? "conjugation" : "vocab");
  };
  form.addEventListener("change", (e) => {
    if (e.target.name === "kind" || e.target.name === "count") syncKindCopy();
  });
  syncKindCopy();
  bindDifficultyPick(form);

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(form);
    const topic = String(fd.get("topic") || "").trim();
    const count = clampGenerateCount(fd.get("count"));
    const kind = selectedKind();
    const difficulty = selectedDifficulty(fd.get("difficulty"));
    if (!topic) return;

    state.generate = { count, difficulty };
    persist();

    submit.disabled = true;
    status.classList.remove("error");
    const started = Date.now();
    const selecting = kind === "conjugation" ? "Verben werden ausgewählt…" : "Wörter werden ausgewählt…";
    const lookingUp = "Definitionen werden nachgeschlagen…";
    const showStatus = () => {
      const s = Math.floor((Date.now() - started) / 1000);
      // Stay on selection, then briefly mention the dictionary lookup.
      status.textContent = s >= 4 && s % 6 >= 4 ? lookingUp : selecting;
    };
    showStatus();
    const tick = setInterval(showStatus, 500);

    const controller = new AbortController();
    const kill = setTimeout(() => controller.abort(), 80_000);

    try {
      const res = await fetch("/api/sets/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ topic, count, kind, difficulty }),
        signal: controller.signal,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(uiMessage(data.error, "Generation failed"));
      if (!data.cards?.length) throw new Error(uiMessage("", "No cards returned — try a broader topic"));

      const set = {
        id: uid(),
        title: topic,
        kind: kind === "conjugation" ? "conjugation" : "vocab",
        difficulty: selectedDifficulty(data.difficulty || difficulty),
        createdAt: data.createdAt || new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        cards: data.cards.map((c) => ({
          id: uid(),
          source: c.source,
          sourceArticle: c.sourceArticle || "",
          target: c.target,
          targetArticle: c.targetArticle || "",
          translations: meaningList(c).slice(0, 3),
          pos: c.pos || "",
          foundInDict: !!c.foundInDict,
          paradigm: c.paradigm || null,
        })),
      };
      state.sets.unshift(set);
      persist();
      const unit = kind === "conjugation" ? "Verben" : "Karten";
      status.textContent = `${set.cards.length} ${unit} erstellt. Wird geöffnet…`;
      location.hash = `#/study/${set.id}`;
    } catch (err) {
      status.classList.add("error");
      if (err?.name === "AbortError") {
        status.textContent = uiMessage("", "Timed out waiting for the AI. Try again in a minute, or set OPENROUTER_MODEL to a faster/paid model.");
      } else {
        status.textContent = uiMessage(err.message, "Something went wrong");
      }
      submit.disabled = false;
    } finally {
      clearInterval(tick);
      clearTimeout(kill);
    }
  });
}

function renderStudy(setId) {
  const set = state.sets.find((s) => s.id === setId);
  if (!set) {
    location.hash = "#/";
    return;
  }

  syncDirectionSetting();
  app.replaceChildren(cloneTemplate("tpl-study"));
  document.querySelector(".view.study .back").href = "#/";

  document.getElementById("study-title").textContent = set.title;
  const progressEl = document.getElementById("study-progress");
  const mastery = document.getElementById("study-mastery");
  const flashcard = document.getElementById("flashcard");
  const questionFace = document.getElementById("card-question");
  const promptLang = document.getElementById("card-prompt-lang");
  const promptLine = document.getElementById("card-prompt-line");
  const promptEl = document.getElementById("card-prompt");
  const answerWrap = document.getElementById("card-answer-wrap");
  const answerLang = document.getElementById("card-answer-lang");
  const answerLine = document.getElementById("card-answer-line");
  const answerEl = document.getElementById("card-answer");
  const answerAlts = document.getElementById("card-answer-alts");
  const answerAltsLabel = document.getElementById("card-answer-alts-label");
  const answerAltsList = document.getElementById("card-answer-alts-list");
  const flipBack = document.getElementById("card-flip-back");
  const dictBtn = document.getElementById("card-dict");
  const dictPop = document.getElementById("card-dict-pop");
  const dictPons = document.getElementById("card-dict-pons");
  const dictLeo = document.getElementById("card-dict-leo");
  const removeCard = document.getElementById("card-remove");
  const hint = document.getElementById("card-hint");
  const actionDock = document.getElementById("action-dock");
  const showAnswer = document.getElementById("show-answer");
  const nextCard = document.getElementById("next-card");
  const studyStatus = document.getElementById("study-status");

  let revealed = false;
  let current = null;
  let counted = false;
  let answerStarted = 0;
  let answerOpen = false;

  function syncProgress() {
    const seen = set.cards.filter((card) => timesSeen(card) > 0).length;
    progressEl.textContent = `${seen} gesehen · ${set.cards.length} gesamt`;
    paintMastery(mastery, set.cards);
  }

  function armAnswerTimer() {
    answerStarted = performance.now();
    answerOpen = true;
  }

  function takeAnswerTime() {
    if (!answerOpen || !current?.card) return;
    answerOpen = false;
    if (!recordSample(current.card, performance.now() - answerStarted)) return;
    persist();
  }

  function placeRemove() {
    (answerWrap.hidden ? promptLine : answerLine).append(removeCard);
  }

  function showDone() {
    current = null;
    revealed = false;
    flashcard.classList.add("idle");
    flashcard.classList.remove("revealed");
    flashcard.tabIndex = -1;
    flashcard.setAttribute("aria-disabled", "true");
    promptLang.textContent = "";
    promptEl.textContent = "Keine Karten";
    questionFace.hidden = false;
    answerWrap.hidden = true;
    placeRemove();
    clearAnswerAlts();
    flipBack.hidden = true;
    updateDictButton();
    removeCard.hidden = true;
    hint.hidden = true;
    setActions("none");
    studyStatus.textContent = "Diese Sammlung ist leer.";
    syncProgress();
  }

  let showToken = 0;

  async function showCard() {
    if (!set.cards.length) {
      showDone();
      return;
    }
    const token = ++showToken;
    const turn = Number.isFinite(set.turn) ? set.turn : 0;
    current = pickCard(set.cards, turn);
    counted = false;
    revealed = false;
    flashcard.classList.remove("idle", "revealed");
    flashcard.tabIndex = 0;
    flashcard.setAttribute("aria-disabled", "false");
    questionFace.hidden = false;
    answerWrap.hidden = true;
    placeRemove();
    flipBack.hidden = true;
    removeCard.hidden = false;
    updateDictButton();
    setActions("question");
    hint.hidden = false;
    hint.textContent = "Antippen zum Aufdecken";
    studyStatus.textContent = "";
    armAnswerTimer();

    const card = current.card;
    const esDe = state.direction !== "de-es";
    paintAnswer(card, esDe);
    syncProgress();
    await ensureTranslations(card);
    if (token !== showToken) return;
    paintAnswer(card, esDe);
    syncProgress();
  }

  function paintAnswer(card, esDe) {
    const meanings = meaningList(card);
    if (esDe) {
      promptLang.textContent = "Spanisch";
      answerLang.textContent = "Deutsch";
      promptEl.textContent = formatWithArticle(card.source, card.sourceArticle);
      answerEl.textContent = formatWithArticle(meanings[0] || card.target, card.targetArticle);
      fillAnswerAlts(meanings.slice(1), "auch");
      updateDictButton();
      return;
    }
    promptLang.textContent = "Deutsch";
    answerLang.textContent = "Spanisch";
    promptEl.textContent = formatWithArticle(card.target, card.targetArticle);
    answerEl.textContent = formatWithArticle(card.source, card.sourceArticle);
    const lines = meanings.map((text, i) => (i === 0 ? formatWithArticle(text, card.targetArticle) : text));
    fillAnswerAlts(meanings.length > 1 ? lines : [], "Bedeutungen");
    updateDictButton();
  }

  function clearAnswerAlts() {
    answerAlts.hidden = true;
    answerAltsLabel.textContent = "";
    answerAltsList.replaceChildren();
  }

  function fillAnswerAlts(lines, label) {
    answerAltsList.replaceChildren();
    if (!lines.length) {
      answerAlts.hidden = true;
      answerAltsLabel.textContent = "";
      return;
    }
    answerAltsLabel.textContent = label;
    for (const text of lines) {
      const li = document.createElement("li");
      li.textContent = text;
      answerAltsList.append(li);
    }
    answerAlts.hidden = false;
  }

  function countReveal() {
    if (!current || counted) return;
    counted = true;
    const turn = Number.isFinite(set.turn) ? set.turn : 0;
    set.turn = markSeen(current.card, turn);
    set.updatedAt = new Date().toISOString();
    persist();
    syncProgress();
  }

  function reveal() {
    if (!current || revealed) return;
    takeAnswerTime();
    countReveal();
    revealed = true;
    flashcard.classList.add("revealed");
    questionFace.hidden = true;
    answerWrap.hidden = false;
    placeRemove();
    flipBack.hidden = false;
    hint.hidden = true;
    updateDictButton();
    setActions("answer");
  }

  function showQuestion() {
    if (!current || !revealed) return;
    revealed = false;
    flashcard.classList.remove("revealed");
    questionFace.hidden = false;
    answerWrap.hidden = true;
    placeRemove();
    flipBack.hidden = true;
    hint.hidden = false;
    updateDictButton();
    setActions("question");
  }

  function setDictOpen(open) {
    dictPop.hidden = !open;
    dictBtn.setAttribute("aria-expanded", open ? "true" : "false");
  }

  function updateDictButton() {
    const card = current?.card;
    const esDe = state.direction !== "de-es";
    const word = card ? (esDe ? meaningList(card)[0] || card.target : card.source || "").trim() : "";
    if (!card || !revealed || !word) {
      dictBtn.hidden = true;
      setDictOpen(false);
      return;
    }
    const q = encodeURIComponent(word).replaceAll("%20", "+");
    const pair = esDe ? "deutsch-spanisch" : "spanisch-deutsch";
    dictPons.href = `https://de.pons.com/übersetzung/${pair}/${q}`;
    dictLeo.href = `https://dict.leo.org/spanisch-deutsch/${q}`;
    dictBtn.hidden = false;
  }

  function setActions(mode) {
    actionDock.hidden = mode === "none";
    showAnswer.hidden = mode !== "question";
    nextCard.hidden = mode !== "answer";
  }

  function advance() {
    if (!current || !revealed) return;
    showCard();
  }

  bindActivation(
    flashcard,
    () => {
      if (!current) return;
      if (!revealed) reveal();
      else advance();
    },
    (e) => e.target.closest(".card-tools, .remove-card"),
  );

  flashcard.addEventListener("keydown", (e) => {
    if (e.target !== flashcard) return;
    if (e.key !== "Enter" && e.key !== " ") return;
    e.preventDefault();
    flashcard.click();
  });

  flipBack.addEventListener("click", (e) => {
    e.stopPropagation();
    showQuestion();
  });

  dictBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    setDictOpen(dictPop.hidden);
  });

  dictPop.addEventListener("click", (e) => {
    e.stopPropagation();
    if (e.target.closest("a")) setDictOpen(false);
  });

  function onDocClickCloseDict() {
    if (!dictPop.hidden) setDictOpen(false);
  }
  document.addEventListener("click", onDocClickCloseDict);

  removeCard.addEventListener("click", (e) => {
    e.stopPropagation();
    if (!current) return;
    const card = current.card;
    const es = formatWithArticle(card.source, card.sourceArticle);
    const de = formatWithArticle(card.target, card.targetArticle);
    if (!confirm(`„${es}“ / „${de}“ aus der Sammlung entfernen?`)) return;
    const idx = set.cards.findIndex((c) => c.id === card.id);
    if (idx < 0) return;
    set.cards.splice(idx, 1);
    set.updatedAt = new Date().toISOString();
    persist();
    showCard();
  });

  bindActivation(showAnswer, () => reveal());
  bindActivation(nextCard, () => advance());

  const modifierKeys = new Set(["Shift", "Control", "Alt", "Meta", "CapsLock", "NumLock", "ScrollLock"]);
  function onStudyKey(e) {
    if (e.repeat || e.metaKey || e.ctrlKey || e.altKey) return;
    const tag = e.target?.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || e.target?.isContentEditable) return;
    if (!current || !settingsPop.hidden || !infoPop.hidden || !addWordDialog.hidden) return;
    if (e.key === "Escape" && !dictPop.hidden) {
      e.preventDefault();
      setDictOpen(false);
      return;
    }
    if (modifierKeys.has(e.key)) return;
    if ((e.key === "Enter" || e.key === " ") && e.target?.closest?.(".card-tools")) return;
    if ((e.key === "Enter" || e.key === " ") && e.target === flashcard) return;
    e.preventDefault();
    if (!revealed) reveal();
    else advance();
  }
  document.addEventListener("keydown", onStudyKey);
  unbindStudyKeys = () => {
    document.removeEventListener("keydown", onStudyKey);
    document.removeEventListener("click", onDocClickCloseDict);
  };
  repaintStudy = () => {
    if (!current) return;
    paintAnswer(current.card, state.direction !== "de-es");
  };

  showCard();
}

function renderConjugation(setId) {
  const set = state.sets.find((s) => s.id === setId);
  if (!set) {
    location.hash = "#/";
    return;
  }

  app.replaceChildren(cloneTemplate("tpl-conj"));
  document.querySelector(".conj-study .back").href = "#/conjugation";
  document.getElementById("conj-title").textContent = set.title;
  const progressEl = document.getElementById("conj-progress");
  const mastery = document.getElementById("conj-mastery");
  const focusEl = document.getElementById("conj-focus");
  const glossEl = document.getElementById("conj-gloss");
  const altsEl = document.getElementById("conj-alts");
  const altsLabel = document.getElementById("conj-alts-label");
  const altsList = document.getElementById("conj-alts-list");
  const nowEl = document.getElementById("conj-now");
  const stack = document.getElementById("conj-stack");
  const hint = document.getElementById("conj-hint");
  const dock = document.getElementById("conj-dock");
  const revealBtn = document.getElementById("conj-reveal");
  const nextBtn = document.getElementById("conj-next");
  const removeBtn = document.getElementById("conj-remove");
  const statusEl = document.getElementById("conj-status");

  let current = null;
  let orderCache = null;
  let allRevealed = false;
  let counted = false;
  let answerStarted = 0;
  let answerOpen = false;

  resetConjugationOrder = () => {
    orderCache = null;
  };

  function syncProgress() {
    const seen = set.cards.filter((card) => timesSeen(card) > 0).length;
    progressEl.textContent = `${seen} gesehen · ${set.cards.length} gesamt`;
    paintMastery(mastery, set.cards);
  }

  function armAnswerTimer() {
    answerStarted = performance.now();
    answerOpen = true;
  }

  function takeAnswerTime() {
    if (!answerOpen || !current?.card) return;
    answerOpen = false;
    if (!recordSample(current.card, performance.now() - answerStarted)) return;
    persist();
  }

  function personSequence(cardId) {
    if (state.conjugation.order !== "random") return PERSONS;
    if (!orderCache || orderCache.id !== cardId) {
      orderCache = { id: cardId, list: shuffle(PERSONS) };
    }
    return orderCache.list;
  }

  function formsFor(card) {
    const paradigm = card.paradigm || {};
    const mode = state.conjugation.mode;
    if (mode === "ger") {
      const forms = [];
      if (paradigm.ger) forms.push({ id: "ger", label: "gerundio", form: paradigm.ger });
      if (paradigm.ptcp) forms.push({ id: "ptcp", label: "participio perfecto", form: paradigm.ptcp });
      return forms;
    }
    const persons = personSequence(card.id);
    if (mode === "imp") {
      const neg = state.conjugation.tense === "neg";
      const row = neg ? paradigm.nimp : paradigm.imp;
      return persons
        .map((p) => {
          const bare = row?.[p.index] || "";
          let label = p.label;
          if (p.index === 2) label = "usted";
          else if (p.index === 5) label = "ustedes";
          return { ...p, label, form: bare && neg ? `no ${bare}` : bare };
        })
        .filter((p) => p.form);
    }
    if (mode === "cond") {
      const row = paradigm.ind?.cond || [];
      return persons.map((p) => ({ ...p, form: row[p.index] || "" })).filter((p) => p.form);
    }
    const table = mode === "sbjv" ? paradigm.sbjv : paradigm.ind;
    const row = table?.[state.conjugation.tense] || [];
    return persons.map((p) => ({ ...p, form: row[p.index] || "" })).filter((p) => p.form);
  }

  function focusLabel() {
    const mode = state.conjugation.mode;
    if (mode === "ger" || mode === "cond") return MODE_LABEL[mode] || "";
    const tense = TENSE_OPTIONS[mode]?.find(([id]) => id === state.conjugation.tense);
    if (!tense) return MODE_LABEL[mode] || "";
    return `${MODE_LABEL[mode] || ""} · ${tense[1]}`;
  }

  function showDone() {
    current = null;
    allRevealed = false;
    focusEl.textContent = "Keine Verben";
    paintMeanings(null);
    nowEl.textContent = "";
    stack.replaceChildren();
    hint.hidden = true;
    dock.hidden = true;
    removeBtn.hidden = true;
    statusEl.textContent = "Diese Sammlung ist leer.";
    syncProgress();
  }

  function renderStack() {
    if (!current) return;
    const card = current.card;
    const forms = formsFor(card);
    stack.replaceChildren();
    allRevealed = forms.length === 0;
    for (const item of forms) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "conj-card";
      btn.setAttribute("aria-expanded", "false");
      btn.innerHTML = `<span class="who">${escapeHtml(item.label)}</span><span class="conj-form" aria-hidden="true">${escapeHtml(item.form)}</span>`;
      btn.addEventListener("click", () => revealCard(btn));
      stack.appendChild(btn);
    }
    hint.hidden = false;
    hint.classList.toggle("is-reserved", allRevealed);
    revealBtn.hidden = allRevealed;
    nextBtn.hidden = !allRevealed;
    dock.hidden = false;
    statusEl.textContent = forms.length ? "" : "Für diese Zeit gibt es keine Formen.";
    nowEl.textContent = focusLabel();
    if (forms.length) armAnswerTimer();
    else answerOpen = false;
  }

  function showCard() {
    if (!set.cards.length) {
      showDone();
      return;
    }
    const turn = Number.isFinite(set.turn) ? set.turn : 0;
    current = pickCard(set.cards, turn);
    counted = false;
    const card = current.card;
    focusEl.textContent = card.source || "";
    paintMeanings(card);
    removeBtn.hidden = false;
    hint.textContent = "Karte antippen, oder unten der Reihe nach aufdecken";
    renderStack();
    syncProgress();
    ensureTranslations(card).then(() => {
      if (current?.card?.id === card.id) paintMeanings(card);
    });
  }

  function paintMeanings(card) {
    const meanings = card ? meaningList(card) : [];
    glossEl.textContent = meanings[0] || "";
    altsList.replaceChildren();
    const rest = meanings.slice(1, 3);
    if (!rest.length) {
      altsEl.hidden = true;
      altsLabel.textContent = "";
      return;
    }
    altsLabel.textContent = "auch";
    for (const text of rest) {
      const li = document.createElement("li");
      li.textContent = text;
      altsList.append(li);
    }
    altsEl.hidden = false;
  }

  function countReveal() {
    if (!current || counted) return;
    counted = true;
    const turn = Number.isFinite(set.turn) ? set.turn : 0;
    set.turn = markSeen(current.card, turn);
    set.updatedAt = new Date().toISOString();
    persist();
    syncProgress();
  }

  function revealCard(btn) {
    if (!current || btn.classList.contains("revealed")) return;
    btn.classList.add("revealed");
    btn.setAttribute("aria-expanded", "true");
    const form = btn.querySelector(".conj-form");
    if (form) form.removeAttribute("aria-hidden");
    if (!stack.querySelector(".conj-card:not(.revealed)")) {
      allRevealed = true;
      takeAnswerTime();
      countReveal();
      revealBtn.hidden = true;
      nextBtn.hidden = false;
      hint.classList.add("is-reserved");
    }
  }

  function revealNext() {
    const next = stack.querySelector(".conj-card:not(.revealed)");
    if (next) revealCard(next);
  }

  function advance() {
    if (!current || !allRevealed) return;
    showCard();
  }

  refreshConjugation = () => {
    if (!current) return;
    renderStack();
  };

  bindActivation(revealBtn, () => revealNext());
  bindActivation(nextBtn, () => advance());
  removeBtn.addEventListener("click", () => {
    if (!current) return;
    const card = current.card;
    const gloss = meaningList(card)[0] || card.target || "";
    if (!confirm(`„${card.source}“${gloss ? ` / „${gloss}“` : ""} aus der Sammlung entfernen?`)) return;
    const idx = set.cards.findIndex((c) => c.id === card.id);
    if (idx < 0) return;
    set.cards.splice(idx, 1);
    set.updatedAt = new Date().toISOString();
    persist();
    showCard();
  });

  const modifierKeys = new Set(["Shift", "Control", "Alt", "Meta", "CapsLock", "NumLock", "ScrollLock"]);
  function onStudyKey(e) {
    if (e.repeat || e.metaKey || e.ctrlKey || e.altKey) return;
    const tag = e.target?.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || e.target?.isContentEditable) return;
    if (!current || !settingsPop.hidden || !infoPop.hidden || !addWordDialog.hidden) return;
    if (modifierKeys.has(e.key)) return;
    e.preventDefault();
    if (!allRevealed) revealNext();
    else advance();
  }
  document.addEventListener("keydown", onStudyKey);
  unbindStudyKeys = () => document.removeEventListener("keydown", onStudyKey);

  refreshConjugationParadigms(set).finally(() => {
    if (openStudySet()?.id !== set.id) return;
    showCard();
  });
}

// Touch emulation (and real touch) follows a tap with a compatibility mouse click.
// That extra click was landing on the same control, or on the button that just
// replaced it, so two taps advanced three steps.
let touchCompatUntil = 0;

function isTouchLike(e) {
  return e.pointerType === "touch" || e.pointerType === "pen";
}

function bindActivation(button, action, ignore) {
  let armed = false;

  button.addEventListener("pointerdown", (e) => {
    if (isTouchLike(e)) touchCompatUntil = performance.now() + 500;
    const compatMouse = !isTouchLike(e) && performance.now() < touchCompatUntil;
    if (e.button !== 0 || ignore?.(e) || compatMouse) {
      armed = false;
      return;
    }
    armed = true;
    const pointerId = e.pointerId;
    const stop = (ev) => {
      if (ev.pointerId !== pointerId) return;
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("pointercancel", stop);
      if (ev.type === "pointercancel") {
        armed = false;
        return;
      }
      const hit = document.elementFromPoint(ev.clientX, ev.clientY);
      const onButton = hit ? button.contains(hit) : !!(ev.target && button.contains(ev.target));
      if (!onButton) {
        armed = false;
        return;
      }
      if (!isTouchLike(ev)) return;
      armed = false;
      if (!button.isConnected || ignore?.(ev)) return;
      action();
    };
    window.addEventListener("pointerup", stop);
    window.addEventListener("pointercancel", stop);
  });

  button.addEventListener("click", (e) => {
    if (!button.isConnected || ignore?.(e)) return;
    if (isTouchLike(e)) return;
    if (e.detail !== 0 && performance.now() < touchCompatUntil) return;
    if (e.detail !== 0) {
      if (!armed) return;
      armed = false;
    }
    action();
  });
}

function shuffle(items) {
  const copy = items.slice();
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [copy[i], copy[j]] = [copy[j], copy[i]];
  }
  return copy;
}

function refreshStudyCardSides() {
  repaintStudy();
}

function openStudySet() {
  const { parts } = parseRoute();
  if (parts[0] !== "study" || !parts[1]) return null;
  return state.sets.find((s) => s.id === parts[1]) || null;
}

function openSet() {
  const { parts } = parseRoute();
  if ((parts[0] !== "study" && parts[0] !== "stats") || !parts[1]) return null;
  return state.sets.find((s) => s.id === parts[1]) || null;
}

function sourceKey(value) {
  return String(value || "").trim().toLocaleLowerCase();
}

function setHasSource(set, source) {
  const key = sourceKey(source);
  if (!key) return false;
  return (set.cards || []).some((card) => sourceKey(card.source) === key);
}

function cardFromSearchResult(hit) {
  const translations = meaningList(hit).slice(0, 3);
  const matched = String(hit.matched || "").trim();
  if (matched) {
    const rest = translations.filter((t) => t.toLowerCase() !== matched.toLowerCase());
    translations.splice(0, translations.length, matched, ...rest.slice(0, 2));
  }
  return {
    id: uid(),
    source: hit.source,
    sourceArticle: hit.sourceArticle || "",
    target: translations[0] || hit.target || "",
    targetArticle: hit.targetArticle || "",
    translations,
    pos: hit.pos || "",
    foundInDict: !!hit.foundInDict,
    paradigm: hit.paradigm || null,
  };
}

function updateOpenSetProgress(set) {
  const progressEl = document.getElementById("study-progress") || document.getElementById("conj-progress");
  const masteryEl = document.getElementById("study-mastery") || document.getElementById("conj-mastery");
  if (progressEl) {
    const seen = set.cards.filter((card) => timesSeen(card) > 0).length;
    progressEl.textContent = `${seen} gesehen · ${set.cards.length} gesamt`;
  }
  if (masteryEl) paintMastery(masteryEl, set.cards);
}

function syncConjugationControls() {
  const modeSel = document.getElementById("conj-mode");
  const tenseSel = document.getElementById("conj-tense");
  const tenseWrap = document.getElementById("settings-tense");
  if (!modeSel || !tenseSel || !tenseWrap) return;
  const c = state.conjugation;
  if (c.mode === "ind" && c.tense === "cond") c.mode = "cond";
  if (!MODE_LABEL[c.mode]) c.mode = "ind";
  if (c.order !== "random") c.order = "default";
  modeSel.value = c.mode;
  const opts = TENSE_OPTIONS[c.mode];
  const needsTense = !!opts;
  tenseWrap.hidden = !needsTense;
  const tenseLabel = tenseWrap.querySelector("span");
  if (tenseLabel) tenseLabel.textContent = c.mode === "imp" ? "Form" : "Zeit";
  if (needsTense) {
    if (!opts.some(([id]) => id === c.tense)) c.tense = opts[0][0];
    tenseSel.replaceChildren(
      ...opts.map(([id, label]) => {
        const opt = document.createElement("option");
        opt.value = id;
        opt.textContent = label;
        return opt;
      }),
    );
    tenseSel.value = c.tense;
  }
  for (const input of settingsPop.querySelectorAll('input[name="conj-order"]')) {
    input.checked = input.value === c.order;
  }
}

function onHomeRoute() {
  const { parts } = parseRoute();
  return parts[0] !== "new" && parts[0] !== "study" && parts[0] !== "stats" && parts[0] !== "share";
}

function shareUrlFor(id) {
  return `${location.origin}${location.pathname}${location.search}#/share/${id}`;
}

function sanitizeSetForShare(set) {
  const payload = {
    id: set.id,
    title: set.title,
    kind: set.kind === "conjugation" ? "conjugation" : "vocab",
    createdAt: set.createdAt || "",
    cards: (set.cards || []).map((card) => ({
      id: card.id,
      source: card.source || "",
      sourceArticle: card.sourceArticle || "",
      target: card.target || "",
      targetArticle: card.targetArticle || "",
      translations: meaningList(card).slice(0, 3),
      pos: card.pos || "",
      foundInDict: !!card.foundInDict,
      paradigm: set.kind === "conjugation" ? card.paradigm || null : null,
    })),
  };
  if (difficultyLabels[set.difficulty]) payload.difficulty = set.difficulty;
  return payload;
}

function setFromShared(shared) {
  const now = new Date().toISOString();
  const set = {
    id: shared.id,
    title: shared.title,
    kind: shared.kind === "conjugation" ? "conjugation" : "vocab",
    createdAt: shared.createdAt || now,
    updatedAt: now,
    cards: (shared.cards || []).map((card) => ({
      id: card.id || uid(),
      source: card.source || "",
      sourceArticle: card.sourceArticle || "",
      target: card.target || "",
      targetArticle: card.targetArticle || "",
      translations: meaningList(card).slice(0, 3),
      pos: card.pos || "",
      foundInDict: !!card.foundInDict,
      paradigm: shared.kind === "conjugation" ? card.paradigm || null : null,
    })),
  };
  if (difficultyLabels[shared.difficulty]) set.difficulty = shared.difficulty;
  return set;
}

async function copyShareLink(id) {
  const url = shareUrlFor(id);
  try {
    await navigator.clipboard.writeText(url);
    showSnackbar("Link kopiert");
    return true;
  } catch {
    window.prompt("Link zum Teilen kopieren:", url);
    return false;
  }
}

async function nativeShareLink(setOrTitle, id) {
  const url = shareUrlFor(id);
  const title = typeof setOrTitle === "string" ? setOrTitle : setOrTitle?.title || "Sammlung";
  if (!navigator.share) return false;
  try {
    await navigator.share({ title, text: `Sammlung „${title}“`, url });
    return true;
  } catch (err) {
    if (err?.name === "AbortError") return true;
    return false;
  }
}

async function shareSet(set) {
  if (!set?.id) return;
  if (!(set.cards || []).length) {
    showSnackbar("Sammlung hat noch keine Wörter", { error: true });
    return;
  }
  try {
    const res = await fetch("/api/sets/share", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(sanitizeSetForShare(set)),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(uiMessage(data.error, "could not store shared set"));
    if (await nativeShareLink(set, set.id)) return;
    await copyShareLink(set.id);
  } catch (err) {
    showSnackbar(uiMessage(err.message, "could not store shared set"), { error: true });
  }
}

function shareKindLabel(kind, count) {
  if (kind === "conjugation") return count === 1 ? "1 Verb" : `${count} Verben`;
  return count === 1 ? "1 Wort" : `${count} Wörter`;
}

function paintShareTable(cards, kind) {
  const table = document.getElementById("share-table");
  const body = document.getElementById("share-body");
  if (!table || !body) return;
  const list = Array.isArray(cards) ? cards : [];
  body.replaceChildren();
  table.hidden = list.length === 0;
  const conj = kind === "conjugation";
  for (const card of list) {
    const row = document.createElement("tr");
    const es = document.createElement("td");
    const esWord = document.createElement("span");
    esWord.className = "stats-word";
    esWord.textContent = conj
      ? card.source || ""
      : formatWithArticle(card.source, card.sourceArticle);
    es.append(esWord);
    const de = document.createElement("td");
    const meanings = meaningList(card);
    if (meanings.length) {
      const primary = document.createElement("span");
      primary.className = "stats-word";
      primary.textContent = conj
        ? meanings[0]
        : formatWithArticle(meanings[0], card.targetArticle);
      de.append(primary);
      if (meanings.length > 1) {
        const alts = document.createElement("span");
        alts.className = "stats-gloss";
        alts.textContent = meanings.slice(1).join(", ");
        de.append(alts);
      }
    }
    row.append(es, de);
    body.append(row);
  }
}

function bindShareImportDock(inlineBtn, onImport) {
  const view = document.querySelector(".view.share");
  const dock = document.getElementById("share-dock");
  const dockBtn = document.getElementById("share-dock-import");
  if (!view || !dock || !dockBtn || !inlineBtn) return;

  view.classList.add("is-importing");
  dock.hidden = false;
  dock.classList.remove("is-visible");
  dockBtn.onclick = onImport;

  const observer = new IntersectionObserver(
    ([entry]) => {
      dock.classList.toggle("is-visible", !entry.isIntersecting);
    },
    { threshold: 0 },
  );
  observer.observe(inlineBtn);

  unbindShareDock = () => {
    observer.disconnect();
    dockBtn.onclick = null;
    dock.classList.remove("is-visible");
    dock.hidden = true;
    view.classList.remove("is-importing");
    unbindShareDock = () => {};
  };
}

function renderShare(shareId) {
  app.replaceChildren(cloneTemplate("tpl-share"));
  const titleEl = document.getElementById("share-title");
  const ledeEl = document.getElementById("share-lede");
  const barEl = document.getElementById("share-bar");
  const metaEl = document.getElementById("share-meta");
  const statusEl = document.getElementById("share-status");
  const actionsEl = document.getElementById("share-actions");

  const existing = state.sets.find((s) => s.id === shareId);
  if (existing) {
    titleEl.textContent = existing.title;
    ledeEl.textContent = "Diese Sammlung ist bei dir schon gespeichert. Es wird nichts geändert.";
    const count = existing.cards?.length ?? 0;
    const level = difficultyLabel(existing.difficulty);
    barEl.hidden = false;
    metaEl.textContent = level
      ? `${shareKindLabel(existing.kind, count)} · ${level}`
      : shareKindLabel(existing.kind, count);
    statusEl.textContent = "";
    actionsEl.replaceChildren(
      Object.assign(document.createElement("a"), {
        className: "btn primary",
        href: `#/study/${existing.id}`,
        textContent: "Öffnen",
      }),
      (() => {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "btn ghost";
        btn.textContent = "Link kopieren";
        btn.addEventListener("click", () => copyShareLink(existing.id));
        return btn;
      })(),
      ...(navigator.share
        ? [(() => {
            const btn = document.createElement("button");
            btn.type = "button";
            btn.className = "btn ghost";
            btn.textContent = "Weitergeben";
            btn.addEventListener("click", () => nativeShareLink(existing, existing.id));
            return btn;
          })()]
        : []),
    );
    paintShareTable(existing.cards, existing.kind);
    return;
  }

  titleEl.textContent = "Geteilte Sammlung";
  ledeEl.textContent = "Sammlung wird geladen…";
  statusEl.textContent = "";
  barEl.hidden = true;

  fetch(`/api/sets/share/${encodeURIComponent(shareId)}`)
    .then(async (res) => {
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(uiMessage(data.error, "shared set not found"));
      return data;
    })
    .then((shared) => {
      if (state.sets.some((s) => s.id === shareId)) {
        renderShare(shareId);
        return;
      }
      const count = shared.cards?.length ?? 0;
      const kind = shared.kind === "conjugation" ? "conjugation" : "vocab";
      const level = difficultyLabel(shared.difficulty);
      titleEl.textContent = shared.title || "Geteilte Sammlung";
      ledeEl.textContent =
        "Du kannst diese Sammlung importieren. Dabei werden nur die Wörter" +
        (kind === "conjugation" ? " und Konjugationen" : "") +
        " übernommen, nicht der Lernfortschritt oder andere Statistiken der teilenden Person. Nach dem Import startest du mit eigenem Fortschritt.";
      barEl.hidden = false;
      metaEl.textContent = level
        ? `${shareKindLabel(kind, count)} · ${level}`
        : shareKindLabel(kind, count);
      statusEl.textContent = "";
      const doImport = () => {
        if (state.sets.some((s) => s.id === shareId)) {
          renderShare(shareId);
          return;
        }
        const set = setFromShared(shared);
        state.sets.unshift(set);
        persist();
        showSnackbar("Sammlung importiert");
        location.hash = `#/study/${set.id}`;
      };
      const importBtn = document.createElement("button");
      importBtn.type = "button";
      importBtn.className = "btn primary";
      importBtn.textContent = "Importieren";
      importBtn.addEventListener("click", doImport);
      actionsEl.replaceChildren(importBtn);
      paintShareTable(shared.cards, kind);
      bindShareImportDock(importBtn, doImport);
    })
    .catch((err) => {
      titleEl.textContent = "Nicht verfügbar";
      ledeEl.textContent = "Diese geteilte Sammlung konnte nicht geladen werden.";
      statusEl.classList.add("error");
      statusEl.textContent = uiMessage(err.message, "shared set not found");
      barEl.hidden = true;
    });
}

function syncSettingsScope() {
  const studying = openStudySet();
  const set = openSet();
  const conj = studying?.kind === "conjugation";
  const home = onHomeRoute();
  const direction = document.getElementById("settings-direction");
  const conjugation = document.getElementById("settings-conj");
  const addWrap = document.getElementById("settings-add");
  const addBtn = document.getElementById("btn-add-word");
  const io = settingsPop.querySelector(".settings-io");
  if (direction) direction.hidden = !studying || conj;
  if (conjugation) conjugation.hidden = !conj;
  if (addWrap) addWrap.hidden = !set;
  if (addBtn && set) {
    addBtn.textContent = set.kind === "conjugation" ? "Verb hinzufügen" : "Wort hinzufügen";
  }
  if (io) io.hidden = !home;
  settingsBtn.hidden = false;
  syncConjugationControls();
}

function currentTheme() {
  return document.documentElement.dataset.theme === "dark" ? "dark" : "light";
}

function applyTheme(theme) {
  const next = theme === "dark" ? "dark" : "light";
  document.documentElement.dataset.theme = next;
  if (themeColorMeta) {
    themeColorMeta.content = next === "dark" ? "#1a1510" : "#eef3ef";
  }
  for (const input of themeInputs) input.checked = input.value === next;
}

function syncThemeSetting() {
  const value = state.theme === "dark" || state.theme === "light" ? state.theme : currentTheme();
  applyTheme(value);
  if (state.theme !== value) state.theme = value;
}

function syncDirectionSetting() {
  const value = state.direction === "de-es" ? "de-es" : "es-de";
  for (const input of directionInputs) input.checked = input.value === value;
}

function setSettingsOpen(open) {
  settingsPop.hidden = !open;
  settingsBtn.setAttribute("aria-expanded", open ? "true" : "false");
}

function setInfoOpen(open) {
  infoPop.hidden = !open;
  infoBtn.setAttribute("aria-expanded", open ? "true" : "false");
}

infoBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  const open = infoPop.hidden;
  setInfoOpen(open);
  if (open) setSettingsOpen(false);
});

infoPop.addEventListener("click", (e) => e.stopPropagation());

settingsBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  const open = settingsPop.hidden;
  setSettingsOpen(open);
  if (open) setInfoOpen(false);
});

settingsPop.addEventListener("click", (e) => e.stopPropagation());

settingsPop.addEventListener("change", (e) => {
  if (!stateReady) return;
  const theme = e.target.closest('input[name="theme"]');
  if (theme) {
    state.theme = theme.value === "dark" ? "dark" : "light";
    applyTheme(state.theme);
    persist();
    return;
  }
  const input = e.target.closest('input[name="direction"]');
  if (input) {
    state.direction = input.value === "de-es" ? "de-es" : "es-de";
    persist();
    refreshStudyCardSides();
    return;
  }
  if (e.target.id === "conj-mode") {
    state.conjugation.mode = MODE_LABEL[e.target.value] ? e.target.value : "ind";
    const opts = TENSE_OPTIONS[state.conjugation.mode];
    if (opts && !opts.some(([id]) => id === state.conjugation.tense)) state.conjugation.tense = opts[0][0];
    persist();
    syncConjugationControls();
    refreshConjugation();
    return;
  }
  if (e.target.id === "conj-tense") {
    const opts = TENSE_OPTIONS[state.conjugation.mode];
    state.conjugation.tense = e.target.value || opts?.[0]?.[0] || "prs";
    persist();
    refreshConjugation();
    return;
  }
  const order = e.target.closest('input[name="conj-order"]');
  if (!order) return;
  state.conjugation.order = order.value === "random" ? "random" : "default";
  persist();
  resetConjugationOrder();
  refreshConjugation();
});

document.addEventListener("click", () => {
  if (!settingsPop.hidden) setSettingsOpen(false);
  if (!infoPop.hidden) setInfoOpen(false);
  closeOpenSetMenu();
});

document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if (!addWordDialog.hidden) {
    setAddWordOpen(false);
    return;
  }
  if (!settingsPop.hidden) setSettingsOpen(false);
  if (!infoPop.hidden) setInfoOpen(false);
  if (openMenuButton) {
    const button = openMenuButton;
    closeOpenSetMenu();
    button.focus();
  }
});

let addWordSearchTimer = 0;
let addWordSearchToken = 0;
let addWordHits = [];

function setAddWordOpen(open) {
  const set = openSet();
  if (open && !set) return;
  addWordDialog.hidden = !open;
  if (!open) {
    clearTimeout(addWordSearchTimer);
    addWordSearchToken += 1;
    addWordInput.value = "";
    addWordHits = [];
    addWordResults.replaceChildren();
    addWordStatus.textContent = "";
    addWordStatus.classList.remove("error");
    return;
  }
  const conj = set.kind === "conjugation";
  addWordTitle.textContent = conj ? "Verb hinzufügen" : "Wort hinzufügen";
  addWordLabel.textContent = conj
    ? "Spanisch oder Deutsch suchen"
    : "Spanisch oder Deutsch suchen";
  addWordInput.placeholder = conj ? "z. B. hablar oder sprechen" : "z. B. casa oder Haus";
  addWordStatus.textContent = "Tippe, um zu suchen…";
  setSettingsOpen(false);
  setInfoOpen(false);
  requestAnimationFrame(() => addWordInput.focus());
}

function paintAddWordResults(set, hits) {
  addWordHits = hits;
  addWordResults.replaceChildren();
  for (const hit of hits) {
    const already = setHasSource(set, hit.source);
    const li = document.createElement("li");
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "add-word-row";
    btn.disabled = already;
    btn.dataset.source = hit.source;
    const text = document.createElement("span");
    const lemma = document.createElement("span");
    lemma.className = "lemma";
    lemma.textContent = formatWithArticle(hit.source, hit.sourceArticle);
    const gloss = document.createElement("span");
    gloss.className = "gloss";
    gloss.textContent = hit.matched || meaningList(hit)[0] || hit.target || "";
    text.append(lemma, gloss);
    const meta = document.createElement("span");
    meta.className = already ? "meta" : "meta addable";
    meta.textContent = already ? "bereits in Sammlung" : "Hinzufügen";
    btn.append(text, meta);
    li.append(btn);
    addWordResults.append(li);
  }
}

async function runAddWordSearch(query) {
  const set = openSet();
  if (!set) {
    setAddWordOpen(false);
    return;
  }
  const q = String(query || "").trim();
  const token = ++addWordSearchToken;
  if (!q) {
    addWordHits = [];
    addWordResults.replaceChildren();
    addWordStatus.classList.remove("error");
    addWordStatus.textContent = "Tippe, um zu suchen…";
    return;
  }
  addWordStatus.classList.remove("error");
  addWordStatus.textContent = "Suche…";
  const kind = set.kind === "conjugation" ? "conjugation" : "vocab";
  try {
    const res = await fetch(
      `/api/dict/search?q=${encodeURIComponent(q)}&kind=${encodeURIComponent(kind)}&limit=20`,
    );
    const data = await res.json().catch(() => ({}));
    if (token !== addWordSearchToken) return;
    if (!res.ok) throw new Error(uiMessage(data.error, "Something went wrong"));
    const hits = Array.isArray(data.results) ? data.results : [];
    paintAddWordResults(set, hits);
    if (!hits.length) {
      addWordStatus.textContent = kind === "conjugation"
        ? "Kein passendes Verb gefunden."
        : "Kein Treffer im Wörterbuch.";
    } else {
      addWordStatus.textContent = `${hits.length} Treffer`;
    }
  } catch (err) {
    if (token !== addWordSearchToken) return;
    addWordHits = [];
    addWordResults.replaceChildren();
    addWordStatus.classList.add("error");
    addWordStatus.textContent = uiMessage(err.message, "Something went wrong");
  }
}

function scheduleAddWordSearch() {
  clearTimeout(addWordSearchTimer);
  addWordSearchTimer = setTimeout(() => {
    runAddWordSearch(addWordInput.value);
  }, 180);
}

function addSearchHitToSet(hit) {
  const set = openSet();
  if (!set || !hit?.source) return false;
  if (set.kind === "conjugation" && !hit.paradigm) {
    showSnackbar("Keine Konjugation für dieses Verb", { error: true });
    return false;
  }
  if (setHasSource(set, hit.source)) {
    paintAddWordResults(set, addWordHits);
    return false;
  }
  const wasEmpty = !(set.cards || []).length;
  set.cards.push(cardFromSearchResult(hit));
  set.updatedAt = new Date().toISOString();
  persist();
  updateOpenSetProgress(set);
  const { parts } = parseRoute();
  const addedLabel = set.kind === "conjugation" ? "Verb hinzugefügt" : "Wort hinzugefügt";
  if (parts[0] === "stats" && parts[1] === set.id) {
    renderStats(set.id);
    paintAddWordResults(set, addWordHits);
    showSnackbar(addedLabel);
    return true;
  }
  if (wasEmpty && parts[0] === "study" && parts[1] === set.id) {
    // Remount study so an empty "Keine Karten" view picks up the first card.
    const keepQuery = addWordInput.value;
    route();
    setAddWordOpen(true);
    addWordInput.value = keepQuery;
    runAddWordSearch(keepQuery);
    showSnackbar(addedLabel);
    return true;
  }
  paintAddWordResults(set, addWordHits);
  showSnackbar(addedLabel);
  return true;
}

btnAddWord.addEventListener("click", (e) => {
  e.stopPropagation();
  setAddWordOpen(true);
});

addWordDialog.addEventListener("click", (e) => {
  if (e.target.closest("[data-add-word-close]")) {
    setAddWordOpen(false);
    return;
  }
  e.stopPropagation();
  const row = e.target.closest(".add-word-row");
  if (!row || row.disabled) return;
  const hit = addWordHits.find((item) => item.source === row.dataset.source);
  if (!hit) return;
  addSearchHitToSet(hit);
});

addWordInput.addEventListener("input", () => scheduleAddWordSearch());
addWordInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    clearTimeout(addWordSearchTimer);
    runAddWordSearch(addWordInput.value);
  }
});

const snackbar = document.getElementById("snackbar");
let snackbarTimer = 0;

function showSnackbar(message, { error = false } = {}) {
  snackbar.textContent = message;
  snackbar.classList.toggle("error", error);
  snackbar.hidden = false;
  clearTimeout(snackbarTimer);
  snackbarTimer = setTimeout(() => {
    snackbar.hidden = true;
  }, 3200);
}

document.getElementById("btn-export").addEventListener("click", () => {
  if (!stateReady) return;
  exportState(state);
  setSettingsOpen(false);
  showSnackbar("Exportiert");
});
document.getElementById("btn-import").addEventListener("change", async (e) => {
  const file = e.target.files?.[0];
  if (!file || !stateReady) {
    e.target.value = "";
    return;
  }
  try {
    const imported = await importState(file);
    if (state.sets.length && !confirm("Alle lokalen Sammlungen durch die importierte Datei ersetzen?")) {
      e.target.value = "";
      return;
    }
    const previousTheme = state.theme;
    state = imported;
    if (!state.theme) state.theme = previousTheme;
    persist();
    syncThemeSetting();
    syncDirectionSetting();
    route();
    setSettingsOpen(false);
    showSnackbar("Importiert");
  } catch (err) {
    showSnackbar(uiMessage(err.message, "Import failed"), { error: true });
  } finally {
    e.target.value = "";
  }
});

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

window.addEventListener("hashchange", route);

function showInitialLoading() {
  const status = document.createElement("div");
  status.className = "initial-loading";
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  const spinner = document.createElement("span");
  spinner.className = "spinner";
  spinner.setAttribute("aria-hidden", "true");
  const text = document.createElement("p");
  text.textContent = "Vokabeln werden geladen...";
  status.append(spinner, text);
  app.replaceChildren(status);
  app.setAttribute("aria-busy", "true");
}

if (!hasStoredState()) showInitialLoading();

loadState().then((loaded) => {
  app.removeAttribute("aria-busy");
  state = loaded;
  stateReady = true;
  syncThemeSetting();
  persist();
  syncDirectionSetting();
  route();
});
