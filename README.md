# Simple Vocabulary

Lightweight Spanish↔German vocabulary trainer. Data stays in the browser (`localStorage`) with optional JSON export/import. A small Go server serves the UI, the FreeDict-based dictionary, and an OpenRouter-backed set generator (swappable AI provider).

## Features

- Vocabulary sets stored locally
- Topic → ~50 words via OpenRouter (free models supported)
- Translations + articles (`el`/`la`, `der`/`die`/`das`) from the integrated dictionary
- Flashcards: tap to reveal, tap again to advance
- Semi-random order: unseen words are equal, then a word stays quiet for a per-level number of turns and becomes steadily more likely
- Direction toggle: Spanish→German / German→Spanish
- Export / import JSON for other devices
- Publish a set to the server and share a link (words only, no progress)

## Quick start (Nix)

```bash
nix develop
cp .env.example .env   # set OPENROUTER_API_KEY
go run ./cmd/server
# open http://localhost:8080
```

## Docker Compose

```bash
cp .env.example .env   # set OPENROUTER_API_KEY
docker compose up --build
```

## Environment

| Variable | Default | Purpose |
|----------|---------|---------|
| `PORT` | `8080` | Listen port |
| `OPENROUTER_API_KEY` | — | Required for set generation |
| `OPENROUTER_MODEL` | `nvidia/nemotron-3.5-lightning:free` | Preferred OpenRouter model |
| `OPENROUTER_FALLBACK_MODELS` | built-in free chain | Models tried on 429/503/timeout |
| `OPENROUTER_PER_MODEL_TIMEOUT_SEC` | `25` | Per-model attempt timeout |
| `OPENROUTER_MAX_ATTEMPTS` | `3` | Max models tried per generate |
| `DICT_PATH` | `data/spa-deu.json` | Dictionary JSON |
| `SHARED_DIR` | `data/shared` | Published shareable sets |
| `WEB_DIR` | `web` | Static frontend |

Without an API key the app still runs for studying existing/imported sets; generation returns 503.

## Dictionary

`data/spa-deu.json` is converted from FreeDict **spa-deu** (~21k headwords). Rebuild:

```bash
# after extracting freedict-spa-deu-*.src.tar.xz
go run ./cmd/convertdict -in path/to/spa-deu.tei -out data/spa-deu.json
```

See `data/LICENSE.dictionary`.

## Swapping the AI provider

Implement `ai.Provider` in `internal/ai` and wire it in `cmd/server/main.go`. OpenRouter is the default (`internal/ai/openrouter.go`).

## API

- `GET /api/health`
- `GET /api/dict/lookup?q=casa`
- `POST /api/sets/generate` `{ "topic": "…", "count": 50 }`
- `POST /api/sets/share` — publish a set (content only) under its UUID
- `GET /api/sets/share/{id}` — fetch a published set
