import assert from "node:assert/strict";
import test from "node:test";
import { MASTERY_LEVEL, formatDuration, formatPercent, masteryRatio, nextStatsSort, sortStatsRows, trimmedMean } from "./stats.js";

test("four views per word is 100 percent", () => {
  assert.equal(MASTERY_LEVEL, 4);
  const cards = [{ exposure: { seen: 1, lastTurn: 0 } }, { exposure: { seen: 0, lastTurn: 0 } }];
  assert.equal(masteryRatio(cards), 1 / 8);
});

test("views past four keep raising the percentage", () => {
  assert.equal(masteryRatio([{ exposure: { seen: 8, lastTurn: 0 } }]), 2);
  assert.equal(masteryRatio([{ exposure: { seen: 5, lastTurn: 0 } }]), 1.25);
  assert.equal(masteryRatio([]), 0);
  assert.equal(masteryRatio([{ srs: { repetitions: 4 } }]), 1);
});

test("trimmed mean drops one outlier on each side", () => {
  assert.equal(trimmedMean([100, 1, 2, 3]), 2.5);
  assert.equal(trimmedMean([4, 4, 4]), 4);
  assert.equal(trimmedMean([10, 30]), 20);
  assert.equal(trimmedMean([7]), 7);
  assert.equal(trimmedMean([]), null);
  assert.equal(trimmedMean([1, "nope", 5, 100]), 5);
});

test("percent label can pass 100", () => {
  assert.match(formatPercent(0.125), /^12,5\s%$/);
  assert.match(formatPercent(1), /^100\s%$/);
  assert.match(formatPercent(1.25), /^125\s%$/);
});

test("header clicks cycle and then restore the original order", () => {
  assert.deepEqual(nextStatsSort(null, "word", "asc"), { key: "word", dir: "asc" });
  assert.deepEqual(nextStatsSort({ key: "word", dir: "asc" }, "word", "asc"), { key: "word", dir: "desc" });
  assert.deepEqual(nextStatsSort({ key: "word", dir: "desc" }, "word", "asc"), { key: null, dir: null });
  assert.deepEqual(nextStatsSort(null, "seen", "desc"), { key: "seen", dir: "desc" });
  assert.deepEqual(nextStatsSort({ key: "seen", dir: "desc" }, "seen", "desc"), { key: "seen", dir: "asc" });
  assert.deepEqual(nextStatsSort({ key: "time", dir: "asc" }, "seen", "desc"), { key: "seen", dir: "desc" });
});

test("stats rows sort by word, views, and time", () => {
  const rows = [
    { index: 0, word: "perro", seen: 2, time: 3000 },
    { index: 1, word: "gato", seen: 3, time: null },
    { index: 2, word: "casa", seen: 2, time: 1000 },
  ];
  assert.deepEqual(sortStatsRows(rows, { key: "word", dir: "asc" }).map((row) => row.word), ["casa", "gato", "perro"]);
  assert.deepEqual(sortStatsRows(rows, { key: "word", dir: "desc" }).map((row) => row.word), ["perro", "gato", "casa"]);
  assert.deepEqual(sortStatsRows(rows, { key: "seen", dir: "desc" }).map((row) => row.word), ["gato", "perro", "casa"]);
  assert.deepEqual(sortStatsRows(rows, { key: "seen", dir: "asc" }).map((row) => row.word), ["perro", "casa", "gato"]);
  assert.deepEqual(sortStatsRows(rows, { key: "time", dir: "desc" }).map((row) => row.word), ["perro", "casa", "gato"]);
  assert.deepEqual(sortStatsRows(rows, { key: "time", dir: "asc" }).map((row) => row.word), ["casa", "perro", "gato"]);
  assert.deepEqual(sortStatsRows(rows, { key: null, dir: null }).map((row) => row.index), [0, 1, 2]);
});

test("duration formatting", () => {
  assert.equal(formatDuration(null), "—");
  assert.equal(formatDuration(1400), "1,4 s");
  assert.equal(formatDuration(12500), "13 s");
  assert.equal(formatDuration(65000), "1:05 min");
});
