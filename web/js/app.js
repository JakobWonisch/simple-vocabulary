import { loadState, saveState, exportState, importState, uid } from "./storage.js";
import { newSRS, review, dueCards, formatWithArticle } from "./srs.js";

const app = document.getElementById("app");
const topActions = document.getElementById("top-actions");
const dirToggle = document.getElementById("dir-toggle");
const dirLeft = document.getElementById("dir-label-left");
const dirRight = document.getElementById("dir-label-right");

let state = loadState();

function persist() {
  saveState(state);
}

function route() {
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
  topActions.hidden = true;
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
      <span class="set-meta">${set.cards?.length ?? 0} cards · ${due} due</span>
    `;
    btn.addEventListener("click", () => {
      location.hash = `#/study/${set.id}`;
    });

    const del = document.createElement("button");
    del.type = "button";
    del.className = "delete";
    del.textContent = "Delete";
    del.addEventListener("click", (e) => {
      e.stopPropagation();
      if (!confirm(`Delete set “${set.title}”?`)) return;
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
      if (state.sets.length && !confirm("Replace all local sets with the imported file?")) {
        e.target.value = "";
        return;
      }
      state = imported;
      persist();
      updateDirToggle();
      renderHome();
    } catch (err) {
      alert(err.message || "Import failed");
    } finally {
      e.target.value = "";
    }
  });
}

function renderCreate() {
  topActions.hidden = true;
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
    status.textContent = "Generating vocabulary… this can take a few seconds.";

    try {
      const res = await fetch("/api/sets/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ topic, count }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "Generation failed");
      if (!data.cards?.length) throw new Error("No cards returned — try a broader topic");

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
      status.textContent = `Created ${set.cards.length} cards. Opening…`;
      location.hash = `#/study/${set.id}`;
    } catch (err) {
      status.classList.add("error");
      status.textContent = err.message || "Something went wrong";
      submit.disabled = false;
    }
  });
}

function renderStudy(setId) {
  const set = state.sets.find((s) => s.id === setId);
  if (!set) {
    location.hash = "#/";
    return;
  }

  topActions.hidden = false;
  updateDirToggle();
  app.replaceChildren(cloneTemplate("tpl-study"));

  document.getElementById("study-title").textContent = set.title;
  const progressEl = document.getElementById("study-progress");
  const flashcard = document.getElementById("flashcard");
  const promptLang = document.getElementById("card-prompt-lang");
  const promptEl = document.getElementById("card-prompt");
  const answerWrap = document.getElementById("card-answer-wrap");
  const answerLang = document.getElementById("card-answer-lang");
  const answerEl = document.getElementById("card-answer");
  const hint = document.getElementById("card-hint");
  const gradeRow = document.getElementById("grade-row");
  const studyStatus = document.getElementById("study-status");

  let queue = dueCards(set.cards);
  let revealed = false;
  let current = null;

  function syncProgress() {
    const due = dueCards(set.cards).length;
    progressEl.textContent = `${due} due · ${set.cards.length} total`;
  }

  function showDone() {
    current = null;
    revealed = false;
    flashcard.disabled = true;
    promptLang.textContent = "";
    promptEl.textContent = "Done for now";
    answerWrap.hidden = true;
    hint.hidden = true;
    gradeRow.hidden = true;
    studyStatus.textContent = "No cards due. Come back later, or switch direction and keep going.";
    syncProgress();
  }

  function showCard() {
    if (!queue.length) {
      showDone();
      return;
    }
    current = queue[0];
    revealed = false;
    flashcard.disabled = false;
    flashcard.classList.remove("revealed");
    answerWrap.hidden = true;
    gradeRow.hidden = true;
    hint.hidden = false;
    hint.textContent = "Tap to reveal";
    studyStatus.textContent = "";

    const card = current.card;
    const esDe = state.direction !== "de-es";
    if (esDe) {
      promptLang.textContent = "Spanish";
      answerLang.textContent = "German";
      promptEl.textContent = formatWithArticle(card.source, card.sourceArticle);
      answerEl.textContent = formatWithArticle(card.target, card.targetArticle);
    } else {
      promptLang.textContent = "German";
      answerLang.textContent = "Spanish";
      promptEl.textContent = formatWithArticle(card.target, card.targetArticle);
      answerEl.textContent = formatWithArticle(card.source, card.sourceArticle);
    }
    syncProgress();
  }

  function reveal() {
    if (!current || revealed) return;
    revealed = true;
    flashcard.classList.add("revealed");
    answerWrap.hidden = false;
    hint.hidden = true;
    gradeRow.hidden = false;
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
    if (!current) return;
    if (!revealed) {
      reveal();
      return;
    }
    // Second tap: advance with a default "good" review.
    grade("good");
  });

  gradeRow.addEventListener("click", (e) => {
    e.stopPropagation();
    const btn = e.target.closest("[data-grade]");
    if (!btn) return;
    grade(btn.dataset.grade);
  });

  showCard();
}

function refreshStudyCardSides() {
  if ((location.hash || "").includes("/study/")) route();
}

function updateDirToggle() {
  const deEs = state.direction === "de-es";
  dirToggle.setAttribute("aria-checked", deEs ? "true" : "false");
  dirLeft.classList.toggle("active", !deEs);
  dirRight.classList.toggle("active", deEs);
}

dirToggle.addEventListener("click", () => {
  state.direction = state.direction === "es-de" ? "de-es" : "es-de";
  persist();
  updateDirToggle();
  refreshStudyCardSides();
});

document.body.addEventListener("click", (e) => {
  const a = e.target.closest("[data-link]");
  if (!a) return;
  // allow default hash navigation
});

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

window.addEventListener("hashchange", route);
updateDirToggle();
route();
