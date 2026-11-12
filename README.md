# Simple Vocabulary

Lightweight Spanish↔German vocabulary trainer. Data stays in the browser (`localStorage`) with optional JSON export/import — or in an optional account that syncs everything. A small Go server serves the UI, the FreeDict-based dictionary, and an OpenRouter-backed set generator (swappable AI provider).

## Features

- Vocabulary sets stored locally
- Optional accounts: sign up with email + verification code; while logged in the complete state (sets, progress, settings) lives server-side and local sets are disregarded. Signup can import the current local data; otherwise new accounts get the same initial collections as local users. Logged-out use is unchanged. Accounts live in SQLite (`ACCOUNTS_DIR/vocab.db`).
- Admin usage page at `/admin` (set `ADMIN_PASSWORD`): users, active/inactive/new in the last n days, collections per user, daily visitors, page views, and action counts. Counting is anonymous and server-side, so adblockers do not affect it: only daily totals are kept; visitors are deduplicated with a hash of IP + user agent and a daily salt, and the salt and hashes are deleted once the day is over.
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
| `ACCOUNTS_DIR` | `data/accounts` | SQLite database (`vocab.db`): accounts, sessions, usage counters |
| `ADMIN_PASSWORD` | — | Enables `/admin` (HTTP Basic auth, any user name) |
| `BREVO_API_KEY` | — | Brevo transactional email for signup codes |
| `BREVO_SENDER_EMAIL` | — | Verified sender address for those emails |
| `BREVO_SENDER_NAME` | — | Optional sender display name |
| `WEB_DIR` | `web` | Static frontend |

Without an API key the app still runs for studying existing/imported sets; generation returns 503.
Without `BREVO_API_KEY`/`BREVO_SENDER_EMAIL`, signup still works but verification
codes are printed to the server log instead of being mailed (dev mode).

## Dictionary

`data/spa-deu.json` is FreeDict **spa-deu**, optionally supplemented with WikDict
**es-de** lemmas FreeDict lacks (FreeDict wins on conflicts). Rebuild:

```bash
# FreeDict only (after extracting freedict-spa-deu-*.src.tar.xz)
go run ./cmd/convertdict -in path/to/spa-deu.tei -out data/spa-deu.json

# FreeDict + WikDict (needs sqlite3 on PATH)
curl -L -o es-de.sqlite3 \
  https://download.wikdict.com/dictionaries/sqlite/2/es-de.sqlite3
go run ./cmd/convertdict \
  -base data/spa-deu.json \
  -wikdict es-de.sqlite3 \
  -out data/spa-deu.json
```

Do not commit the raw WikDict SQLite file. See `data/LICENSE.dictionary`.

## Conjugations

`data/spa-conj.json` is built from the Kaikki.org Spanish **verb** extract (Wiktextract / English Wiktionary), filtered to FreeDict headwords. Rebuild:

```bash
curl -L -o kaikki-es-verbs.jsonl.gz \
  https://kaikki.org/dictionary/Spanish/pos-verb/kaikki.org-dictionary-Spanish-by-pos-verb.jsonl.gz

go run ./cmd/convertconj \
  -format kaikki \
  -in kaikki-es-verbs.jsonl.gz \
  -dict data/spa-deu.json \
  -out data/spa-conj.json
```

Do not commit the raw JSONL (see `.gitignore`). See `data/LICENSE.conjugation`.

UniMorph TSV remains supported via `-format unimorph` but lacks common irregulars (querer, tener, venir, …).

## Swapping the AI provider

Implement `ai.Provider` in `internal/ai` and wire it in `cmd/server/main.go`. OpenRouter is the default (`internal/ai/openrouter.go`).

## API

- `GET /api/health`
- `GET /api/dict/lookup?q=casa`
- `POST /api/sets/generate` `{ "topic": "…", "count": 50 }`
- `POST /api/sets/share` — publish a set (content only) under its UUID
- `GET /api/sets/share/{id}` — fetch a published set
- `POST /api/auth/signup` `{ "email", "password", "copyLocal", "state" }` — creates pending account, mails a code
- `POST /api/auth/verify` `{ "email", "code" }` — activates account, sets session cookie
- `POST /api/auth/resend` `{ "email" }` — new code for a pending account
- `POST /api/auth/login` `{ "email", "password" }` — session cookie + account state
- `POST /api/auth/logout`
- `GET /api/auth/me` — `{ "email" }` or 401
- `GET /api/account/state` — full state blob of the session account (`X-State-Rev` header carries the revision)
- `PUT|POST /api/account/state` — replace it (POST for `sendBeacon` flush); `?baseRev=N` makes the write compare-and-swap → 409 `{ "error", "rev" }` on a stale base
- `GET /api/account/events` — SSE stream of `{ "rev", "src" }` state-change events for the session account; the `init` event carries the current rev and this session's `src` id so clients can ignore their own writes
- `POST /api/events` `{ "counts": { "card_studied": 3 } }` — anonymous frontend action counts (allowlisted names)
- `GET /admin?days=30` — admin usage page (Basic auth with `ADMIN_PASSWORD`)
- `GET /api/admin/stats?days=30` — same data as JSON

### Querying the database directly

```bash
sqlite3 data/accounts/vocab.db \
  "SELECT count(*) FROM users WHERE verified = 1 AND last_seen_at >= datetime('now','-30 day')"
```

In Docker: `docker compose exec vocab sh` has no `sqlite3`; copy the file out with `docker compose cp vocab:/app/data/accounts/vocab.db .` (also copy `vocab.db-wal` if present).
