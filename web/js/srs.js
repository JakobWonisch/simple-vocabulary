/**
 * Lightweight SM-2 style spaced repetition.
 * grade: again | hard | good | easy
 */
export function newSRS() {
  return {
    ease: 2.5,
    interval: 0,
    repetitions: 0,
    due: Date.now(),
  };
}

export function review(srs, grade) {
  const next = { ...srs };
  const now = Date.now();

  if (grade === "again") {
    next.repetitions = 0;
    next.interval = 0;
    next.ease = Math.max(1.3, next.ease - 0.2);
    next.due = now + 60_000; // 1 minute
    return next;
  }

  if (next.repetitions === 0) {
    next.interval = grade === "easy" ? 4 : 1; // days
  } else if (next.repetitions === 1) {
    next.interval = grade === "easy" ? 7 : grade === "hard" ? 3 : 6;
  } else {
    const hardFactor = grade === "hard" ? 0.8 : 1;
    const easyBonus = grade === "easy" ? 1.3 : 1;
    next.interval = Math.max(1, Math.round(next.interval * next.ease * hardFactor * easyBonus));
  }

  if (grade === "hard") next.ease = Math.max(1.3, next.ease - 0.15);
  if (grade === "good") next.ease = next.ease + 0.0;
  if (grade === "easy") next.ease = next.ease + 0.15;

  next.repetitions += 1;
  next.due = now + next.interval * 86_400_000;
  return next;
}

export function dueCards(cards, now = Date.now()) {
  return cards
    .map((c, index) => ({ card: c, index }))
    .filter(({ card }) => !card.srs || card.srs.due <= now)
    .sort((a, b) => (a.card.srs?.due ?? 0) - (b.card.srs?.due ?? 0));
}

export function formatWithArticle(word, article) {
  if (!word) return "";
  if (!article) return word;
  return `${article} ${word}`;
}
