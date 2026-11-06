import { loadState, saveState, exportState, importState, uid } from "./storage.js";
import { newSRS, review, dueCards, formatWithArticle } from "./srs.js";

const app = document.getElementById("app");
const settingsBtn = document.getElementById("settings-btn");
const settingsPop = document.getElementById("settings-pop");
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
  "AI providers are rate-limited right now. Wait a minute and retry, or set OPENROUTER_MODEL to another model.":
    "Die KI-Anbieter sind gerade ausgelastet. Warte eine Minute und versuche es erneut, oder setze OPENROUTER_MODEL auf ein anderes Modell.",
  "AI rate limit hit. Retry shortly, or change OPENROUTER_MODEL.":
    "KI-Limit erreicht. Versuche es gleich erneut oder ändere OPENROUTER_MODEL.",
};

function uiMessage(message, fallback) {
  if (message && knownErrors[message]) return knownErrors[message];
  if (message) return message;
  return knownErrors[fallback] || fallback;
}

let state = loadState();
let unbindStudyKeys = () => {};

function persist() {
  saveState(state);
}

function route() {
  unbindStudyKeys();
  unbindStudyKeys = () => {};
  const hash = location.hash.replace(/^#/, "") || "/";
  const parts = hash.split("/").filter(Boolean);

  if (parts[0] === "new") {
    renderCreate();
    return;
  }
  if (parts[0] === "study" && parts[1]) {
    renderStudy(parts[1]);
    return;
  }
  renderHome();
}

function cloneTemplate(id) {
  const tpl = document.getElementById(id);
  return tpl.content.cloneNode(true);
}

function renderHome() {
  app.replaceChildren(cloneTemplate("tpl-home"));

  const list = document.getElementById("set-list");
  const empty = document.getElementById("empty-sets");
  const sets = [...state.sets].sort((a, b) => (b.updatedAt || "").localeCompare(a.updatedAt || ""));

  empty.hidden = sets.length > 0;
  for (const set of sets) {
    const li = document.createElement("li");
    const due = dueCards(set.cards || []).length;
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "set-card";
    btn.innerHTML = `
      <strong>${escapeHtml(set.title)}</strong>
      <span class="set-meta">${set.cards?.length ?? 0} Karten · ${due} fällig</span>
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

  document.getElementById("btn-export").addEventListener("click", () => exportState(state));
  document.getElementById("btn-import").addEventListener("change", async (e) => {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      const imported = await importState(file);
      if (state.sets.length && !confirm("Alle lokalen Sammlungen durch die importierte Datei ersetzen?")) {
        e.target.value = "";
        return;
      }
      state = imported;
      persist();
      syncDirectionSetting();
      renderHome();
    } catch (err) {
      alert(uiMessage(err.message, "Import failed"));
    } finally {
      e.target.value = "";
    }
  });
}

function renderCreate() {
  app.replaceChildren(cloneTemplate("tpl-new"));
  const form = document.getElementById("create-form");
  const status = document.getElementById("create-status");
  const submit = document.getElementById("create-submit");

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(form);
    const topic = String(fd.get("topic") || "").trim();
    const count = Number(fd.get("count") || 50);
    if (!topic) return;

    submit.disabled = true;
    status.classList.remove("error");
    const started = Date.now();
    const tick = setInterval(() => {
      const s = Math.round((Date.now() - started) / 1000);
      status.textContent = `Vokabeln werden erzeugt… ${s}s (probiert einige kostenlose Modelle, falls eines ausgelastet ist)`;
    }, 500);

    const controller = new AbortController();
    const kill = setTimeout(() => controller.abort(), 80_000);

    try {
      const res = await fetch("/api/sets/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ topic, count }),
        signal: controller.signal,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(uiMessage(data.error, "Generation failed"));
      if (!data.cards?.length) throw new Error(uiMessage("", "No cards returned — try a broader topic"));

      const set = {
        id: uid(),
        title: topic,
        createdAt: data.createdAt || new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        cards: data.cards.map((c) => ({
          id: uid(),
          source: c.source,
          sourceArticle: c.sourceArticle || "",
          target: c.target,
          targetArticle: c.targetArticle || "",
          pos: c.pos || "",
          foundInDict: !!c.foundInDict,
          srs: newSRS(),
        })),
      };
      state.sets.unshift(set);
      persist();
      status.textContent = `${set.cards.length} Karten erstellt. Wird geöffnet…`;
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

  document.getElementById("study-title").textContent = set.title;
  const progressEl = document.getElementById("study-progress");
  const flashcard = document.getElementById("flashcard");
  const questionFace = document.getElementById("card-question");
  const promptLang = document.getElementById("card-prompt-lang");
  const promptEl = document.getElementById("card-prompt");
  const answerWrap = document.getElementById("card-answer-wrap");
  const answerLang = document.getElementById("card-answer-lang");
  const answerEl = document.getElementById("card-answer");
  const flipBack = document.getElementById("card-flip-back");
  const removeCard = document.getElementById("card-remove");
  const hint = document.getElementById("card-hint");
  const actionDock = document.getElementById("action-dock");
  const showAnswer = document.getElementById("show-answer");
  const gradeRow = document.getElementById("grade-row");
  const studyStatus = document.getElementById("study-status");

  let queue = dueCards(set.cards);
  let revealed = false;
  let current = null;

  function syncProgress() {
    const due = dueCards(set.cards).length;
    progressEl.textContent = `${due} fällig · ${set.cards.length} gesamt`;
  }

  function showDone() {
    current = null;
    revealed = false;
    flashcard.classList.add("idle");
    flashcard.classList.remove("revealed");
    flashcard.tabIndex = -1;
    flashcard.setAttribute("aria-disabled", "true");
    promptLang.textContent = "";
    promptEl.textContent = "Fürs Erste geschafft";
    questionFace.hidden = false;
    answerWrap.hidden = true;
    flipBack.hidden = true;
    removeCard.hidden = true;
    hint.hidden = true;
    setActions("none");
    studyStatus.textContent = "Keine Karten fällig. Komm später wieder oder wechsle die Fragerichtung und mach weiter.";
    syncProgress();
  }

  function showCard() {
    if (!queue.length) {
      showDone();
      return;
    }
    current = queue[0];
    revealed = false;
    flashcard.classList.remove("idle", "revealed");
    flashcard.tabIndex = 0;
    flashcard.setAttribute("aria-disabled", "false");
    questionFace.hidden = false;
    answerWrap.hidden = true;
    flipBack.hidden = true;
    removeCard.hidden = false;
    setActions("question");
    hint.hidden = false;
    hint.textContent = "Antippen zum Aufdecken";
    studyStatus.textContent = "";

    const card = current.card;
    const esDe = state.direction !== "de-es";
    if (esDe) {
      promptLang.textContent = "Spanisch";
      answerLang.textContent = "Deutsch";
      promptEl.textContent = formatWithArticle(card.source, card.sourceArticle);
      answerEl.textContent = formatWithArticle(card.target, card.targetArticle);
    } else {
      promptLang.textContent = "Deutsch";
      answerLang.textContent = "Spanisch";
      promptEl.textContent = formatWithArticle(card.target, card.targetArticle);
      answerEl.textContent = formatWithArticle(card.source, card.sourceArticle);
    }
    syncProgress();
  }

  function reveal() {
    if (!current || revealed) return;
    revealed = true;
    flashcard.classList.add("revealed");
    questionFace.hidden = true;
    answerWrap.hidden = false;
    flipBack.hidden = false;
    hint.hidden = true;
    setActions("answer");
  }

  function showQuestion() {
    if (!current || !revealed) return;
    revealed = false;
    flashcard.classList.remove("revealed");
    questionFace.hidden = false;
    answerWrap.hidden = true;
    flipBack.hidden = true;
    hint.hidden = false;
    setActions("question");
  }

  function setActions(mode) {
    actionDock.hidden = mode === "none";
    showAnswer.hidden = mode !== "question";
    gradeRow.hidden = mode !== "answer";
  }

  function grade(g) {
    if (!current || !revealed) return;
    const idx = current.index;
    set.cards[idx].srs = review(set.cards[idx].srs || newSRS(), g);
    set.updatedAt = new Date().toISOString();
    persist();
    queue = dueCards(set.cards);
    showCard();
  }

  flashcard.addEventListener("click", () => {
    if (!current || revealed) return;
    reveal();
  });

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
    queue = dueCards(set.cards);
    showCard();
  });

  showAnswer.addEventListener("click", () => reveal());

  gradeRow.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-grade]");
    if (!btn) return;
    grade(btn.dataset.grade);
  });

  const gradesByKey = { 1: "again", 2: "hard", 3: "good", 4: "easy" };
  const modifierKeys = new Set(["Shift", "Control", "Alt", "Meta", "CapsLock", "NumLock", "ScrollLock"]);
  function onStudyKey(e) {
    if (e.repeat || e.metaKey || e.ctrlKey || e.altKey) return;
    const tag = e.target?.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || e.target?.isContentEditable) return;
    if (!current || !settingsPop.hidden) return;

    if (!revealed) {
      if (modifierKeys.has(e.key)) return;
      if ((e.key === "Enter" || e.key === " ") && e.target?.closest?.(".card-tools")) return;
      e.preventDefault();
      reveal();
      return;
    }

    const g = gradesByKey[e.key];
    if (!g) return;
    e.preventDefault();
    grade(g);
  }
  document.addEventListener("keydown", onStudyKey);
  unbindStudyKeys = () => document.removeEventListener("keydown", onStudyKey);

  showCard();
}

function refreshStudyCardSides() {
  if ((location.hash || "").includes("/study/")) route();
}

function syncDirectionSetting() {
  const value = state.direction === "de-es" ? "de-es" : "es-de";
  for (const input of directionInputs) input.checked = input.value === value;
}

function setSettingsOpen(open) {
  settingsPop.hidden = !open;
  settingsBtn.setAttribute("aria-expanded", open ? "true" : "false");
}

settingsBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  setSettingsOpen(settingsPop.hidden);
});

settingsPop.addEventListener("click", (e) => e.stopPropagation());

settingsPop.addEventListener("change", (e) => {
  const input = e.target.closest('input[name="direction"]');
  if (!input) return;
  state.direction = input.value === "de-es" ? "de-es" : "es-de";
  persist();
  refreshStudyCardSides();
});

document.addEventListener("click", () => {
  if (!settingsPop.hidden) setSettingsOpen(false);
});

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !settingsPop.hidden) setSettingsOpen(false);
});

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

window.addEventListener("hashchange", route);
syncDirectionSetting();
route();
