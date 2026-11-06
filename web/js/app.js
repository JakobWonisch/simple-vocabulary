import { loadState, saveState, exportState, importState, uid, hasStoredState } from "./storage.js";
import { pickCard, markSeen, timesSeen, formatWithArticle } from "./memory.js";

const app = document.getElementById("app");
const settingsBtn = document.getElementById("settings-btn");
const settingsPop = document.getElementById("settings-pop");
const infoBtn = document.getElementById("info-btn");
const infoPop = document.getElementById("info-pop");
const directionInputs = settingsPop.querySelectorAll('input[name="direction"]');

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
  "AI providers are rate-limited right now. Wait a minute and retry, or set OPENROUTER_MODEL to another model.":
    "Die KI-Anbieter sind gerade ausgelastet. Warte eine Minute und versuche es erneut, oder setze OPENROUTER_MODEL auf ein anderes Modell.",
  "AI rate limit hit. Retry shortly, or change OPENROUTER_MODEL.":
    "KI-Limit erreicht. Versuche es gleich erneut oder ändere OPENROUTER_MODEL.",
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
  direction: "es-de",
  conjugation: { mode: "ind", tense: "prs", order: "default" },
  sets: [],
};
let stateReady = false;
let unbindStudyKeys = () => {};
let refreshConjugation = () => {};
let resetConjugationOrder = () => {};
let repaintStudy = () => {};

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
    ["pfv", "Pretérito"],
    ["ipfv", "Imperfecto"],
    ["fut", "Futuro"],
    ["cond", "Condicional"],
  ],
  sbjv: [
    ["prs", "Presente"],
    ["ra", "Imperfecto (-ra)"],
    ["se", "Imperfecto (-se)"],
    ["fut", "Futuro"],
  ],
};

const MODE_LABEL = {
  ind: "Indikativ",
  sbjv: "Subjuntivo",
  imp: "Imperativ",
  ger: "Gerundium",
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

function homeHash(kind) {
  return kind === "conjugation" ? "#/conjugation" : "#/";
}

function route() {
  if (!stateReady) return;
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

function fillSetList(list, empty, sets, kind) {
  empty.hidden = sets.length > 0;
  for (const set of sets) {
    const li = document.createElement("li");
    const count = set.cards?.length ?? 0;
    const seen = (set.cards || []).filter((card) => timesSeen(card) > 0).length;
    const kindLabel = kind === "conjugation" ? `${count} Verben` : `${count} Karten`;
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "set-card";
    btn.innerHTML = `
      <strong>${escapeHtml(set.title)}</strong>
      <span class="set-meta">${kindLabel} · ${seen} gesehen</span>
    `;
    btn.addEventListener("click", () => {
      location.hash = `#/study/${set.id}`;
    });

    const del = document.createElement("button");
    del.type = "button";
    del.className = "delete";
    del.textContent = "Löschen";
    del.addEventListener("click", (e) => {
      e.stopPropagation();
      if (!confirm(`Sammlung „${set.title}“ löschen?`)) return;
      state.sets = state.sets.filter((s) => s.id !== set.id);
      persist();
      renderHome();
    });
    btn.appendChild(del);
    li.appendChild(btn);
    list.appendChild(li);
  }
}

function renderCreate(kindHint) {
  app.replaceChildren(cloneTemplate("tpl-new"));
  const form = document.getElementById("create-form");
  const status = document.getElementById("create-status");
  const submit = document.getElementById("create-submit");
  const back = document.querySelector(".view.create .back");

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
    lede.textContent = conj
      ? "Beschreibe ein Thema. Wir erzeugen spanische Verben dazu und behalten nur die, die im Wörterbuch als Verb stehen."
      : "Beschreibe ein Thema. Wir erzeugen etwa 50 spanische Wörter und schlagen die deutschen Übersetzungen im eingebauten Wörterbuch nach.";
    countLabel.textContent = conj ? "Verben" : "Wortanzahl";
    if (back) back.href = homeHash(conj ? "conjugation" : "vocab");
  };
  form.addEventListener("change", (e) => {
    if (e.target.name === "kind") syncKindCopy();
  });
  syncKindCopy();

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(form);
    const topic = String(fd.get("topic") || "").trim();
    const count = Number(fd.get("count") || 50);
    const kind = selectedKind();
    if (!topic) return;

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
        body: JSON.stringify({ topic, count, kind }),
        signal: controller.signal,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(uiMessage(data.error, "Generation failed"));
      if (!data.cards?.length) throw new Error(uiMessage("", "No cards returned — try a broader topic"));

      const set = {
        id: uid(),
        title: topic,
        kind: kind === "conjugation" ? "conjugation" : "vocab",
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

  function syncProgress() {
    const seen = set.cards.filter((card) => timesSeen(card) > 0).length;
    progressEl.textContent = `${seen} gesehen · ${set.cards.length} gesamt`;
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
    set.turn = markSeen(set.cards[current.index], turn);
    set.updatedAt = new Date().toISOString();
    persist();
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

  function reveal() {
    if (!current || revealed) return;
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
    if (!current || !settingsPop.hidden || !infoPop.hidden) return;
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

  resetConjugationOrder = () => {
    orderCache = null;
  };

  function syncProgress() {
    const seen = set.cards.filter((card) => timesSeen(card) > 0).length;
    progressEl.textContent = `${seen} gesehen · ${set.cards.length} gesamt`;
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
      return paradigm.ger ? [{ id: "ger", label: "gerundio", form: paradigm.ger }] : [];
    }
    const persons = personSequence(card.id);
    if (mode === "imp") {
      return persons
        .map((p) => ({ ...p, form: paradigm.imp?.[p.index] || "" }))
        .filter((p) => p.form);
    }
    const table = mode === "sbjv" ? paradigm.sbjv : paradigm.ind;
    const row = table?.[state.conjugation.tense] || [];
    return persons.map((p) => ({ ...p, form: row[p.index] || "" })).filter((p) => p.form);
  }

  function focusLabel() {
    const mode = state.conjugation.mode;
    if (mode === "imp" || mode === "ger") return MODE_LABEL[mode] || "";
    const tense = TENSE_OPTIONS[mode]?.find(([id]) => id === state.conjugation.tense);
    return `${MODE_LABEL[mode] || ""} · ${tense ? tense[1] : ""}`;
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
  }

  function showCard() {
    if (!set.cards.length) {
      showDone();
      return;
    }
    const turn = Number.isFinite(set.turn) ? set.turn : 0;
    current = pickCard(set.cards, turn);
    set.turn = markSeen(set.cards[current.index], turn);
    set.updatedAt = new Date().toISOString();
    persist();
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

  function revealCard(btn) {
    if (!current || btn.classList.contains("revealed")) return;
    btn.classList.add("revealed");
    btn.setAttribute("aria-expanded", "true");
    const form = btn.querySelector(".conj-form");
    if (form) form.removeAttribute("aria-hidden");
    if (!stack.querySelector(".conj-card:not(.revealed)")) {
      allRevealed = true;
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
    if (!current || !settingsPop.hidden || !infoPop.hidden) return;
    if (modifierKeys.has(e.key)) return;
    e.preventDefault();
    if (!allRevealed) revealNext();
    else advance();
  }
  document.addEventListener("keydown", onStudyKey);
  unbindStudyKeys = () => document.removeEventListener("keydown", onStudyKey);

  showCard();
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

function syncConjugationControls() {
  const modeSel = document.getElementById("conj-mode");
  const tenseSel = document.getElementById("conj-tense");
  const tenseWrap = document.getElementById("settings-tense");
  if (!modeSel || !tenseSel || !tenseWrap) return;
  const c = state.conjugation;
  if (!MODE_LABEL[c.mode]) c.mode = "ind";
  if (c.order !== "random") c.order = "default";
  modeSel.value = c.mode;
  const needsTense = c.mode === "ind" || c.mode === "sbjv";
  tenseWrap.hidden = !needsTense;
  if (needsTense) {
    const opts = TENSE_OPTIONS[c.mode];
    if (!opts.some(([id]) => id === c.tense)) c.tense = "prs";
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

function syncSettingsScope() {
  const set = openStudySet();
  const conj = set?.kind === "conjugation";
  const direction = document.getElementById("settings-direction");
  const conjugation = document.getElementById("settings-conj");
  if (direction) direction.hidden = !set || conj;
  if (conjugation) conjugation.hidden = !conj;
  syncConjugationControls();
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
    if (opts && !opts.some(([id]) => id === state.conjugation.tense)) state.conjugation.tense = "prs";
    persist();
    syncConjugationControls();
    refreshConjugation();
    return;
  }
  if (e.target.id === "conj-tense") {
    state.conjugation.tense = e.target.value || "prs";
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
});

document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if (!settingsPop.hidden) setSettingsOpen(false);
  if (!infoPop.hidden) setInfoOpen(false);
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
    state = imported;
    persist();
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
  syncDirectionSetting();
  route();
});
