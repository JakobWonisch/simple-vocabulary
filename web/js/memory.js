/**
 * Semi-random study order.
 *
 * A card's draw weight depends on how many times it has been shown (its level)
 * and how many study turns have passed since that showing. Never-shown cards
 * share one flat weight. A shown card stays at weight 0 for `after` turns,
 * then its weight rises by `slope` on every further turn.
 *
 * Counting: a card shown at turn T is first eligible when the picker runs at
 * turn T + after (weight = slope), then slope * 2 at the next turn, and so on.
 *
 * Levels 1–4 are the again / hard / medium / easy rungs. Later levels keep
 * stretching by about ×2.2, close to the old ease factor, but in turns.
 * Tweak `after` and `slope` per level; past the table, `after` keeps growing
 * by the same ratio as the last step and `slope` stays on the last value.
 */
export const UNSEEN_WEIGHT = 1;

export const LEVELS = [
  { after: 5, slope: 1 },
  { after: 10, slope: 1 },
  { after: 20, slope: 1 },
  { after: 45, slope: 1 },
  { after: 100, slope: 1 },
  { after: 220, slope: 1 },
  { after: 480, slope: 1 },
  { after: 1050, slope: 1 },
];

export function levelConfig(seen) {
  const index = seen - 1;
  if (index < LEVELS.length) return LEVELS[index];
  const last = LEVELS[LEVELS.length - 1];
  const prev = LEVELS[LEVELS.length - 2];
  const ratio = prev.after > 0 ? last.after / prev.after : 2;
  const steps = index - (LEVELS.length - 1);
  return {
    after: Math.round(last.after * ratio ** steps),
    slope: last.slope,
  };
}

export function exposureOf(card) {
  const stored = card?.exposure;
  if (stored && Number.isFinite(stored.seen)) {
    return {
      seen: stored.seen,
      lastTurn: Number.isFinite(stored.lastTurn) ? stored.lastTurn : 0,
    };
  }
  const seen = Number(card?.srs?.repetitions) || 0;
  if (seen <= 0) return { seen: 0, lastTurn: 0 };
  return { seen, lastTurn: -levelConfig(seen).after };
}

export function timesSeen(card) {
  return exposureOf(card).seen;
}

export function cardWeight(card, turn) {
  const { seen, lastTurn } = exposureOf(card);
  if (seen <= 0) return UNSEEN_WEIGHT;
  const { after, slope } = levelConfig(seen);
  const steps = turn - lastTurn - after + 1;
  if (steps <= 0 || slope <= 0) return 0;
  return steps * slope;
}

export function markSeen(card, turn) {
  const seen = exposureOf(card).seen + 1;
  card.exposure = { seen, lastTurn: turn };
  if (card.srs) delete card.srs;
  return turn + 1;
}

export function pickCard(cards, turn, random = Math.random) {
  const weights = cards.map((card) => cardWeight(card, turn));
  const total = weights.reduce((sum, weight) => sum + weight, 0);
  if (total > 0) {
    let cursor = random() * total;
    for (let i = 0; i < cards.length; i++) {
      cursor -= weights[i];
      if (cursor < 0) return { card: cards[i], index: i };
    }
    const index = cards.length - 1;
    return { card: cards[index], index };
  }

  let best = -Infinity;
  const tied = [];
  for (let i = 0; i < cards.length; i++) {
    const { seen, lastTurn } = exposureOf(cards[i]);
    const score = seen <= 0 ? 0 : turn - lastTurn - levelConfig(seen).after;
    if (score > best) {
      best = score;
      tied.length = 0;
      tied.push(i);
    } else if (score === best) {
      tied.push(i);
    }
  }
  const index = tied[Math.floor(random() * tied.length)] ?? 0;
  return { card: cards[index], index };
}

export function formatWithArticle(word, article) {
  if (!word) return "";
  if (!article) return word;
  return `${article} ${word}`;
}
