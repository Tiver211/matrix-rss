# matrixrss

Matrix bot that publishes rooms as RSS/Atom/JSON feeds and exposes the stored messages over an HTTP API.
Encrypted rooms are supported (pure Go Olm, no cgo).

## Usage

1. Copy `.env.example` to `.env` and fill it in.
2. Start the bot (`matrixrss` or `matrixrss serve`) and invite it to a room **as one of `MATRIX_ADMINS`** (other invitations are ignored).
3. In the room send `!rss on`. The bot answers with the feed URL (shown only once, see Credentials).

Who may do what in chat:

- **Bot admins** (`MATRIX_ADMINS`) may do everything: invite the bot, publish rooms and manage every token and key.
- **Room admins** are members whose power level in a room is at least `MATRIX_ROOM_ADMIN_LEVEL` (default 100 = admin; 50 = moderator; 0 = feature off). They manage the feed tokens and API keys **of that room only**. They cannot publish or unpublish rooms and cannot invite the bot.

| Command | Who | What it does |
| --- | --- | --- |
| `!rss on` / `!rss off` | bot admins | publish the room / stop recording and hide the feed |
| `!rss rotate` | room admins | revoke all feed URLs of this room, show a new one |
| `!rss token list [room_id]` | room admins | list the feed tokens of a room |
| `!rss token revoke <id>` | room admins | revoke a feed token of their room |
| `!rss token create <room_id> <name>` | room admins | issue another feed token, e.g. for one more reader (**private chat only**) |
| `!rss key list` / `!rss key revoke <id>` | room admins | list / revoke API keys; a room admin sees and revokes only keys limited to rooms they administer |
| `!rss key create <name> [rooms=!a:hs,!b:hs] [expires=30d]` | room admins | issue a read-only API key (**private chat only**); a room admin's key must be limited with `rooms=` to rooms they administer |

Messages sent after `!rss on` are recorded live. Edits are kept as revisions, deletions as deletion records (see below).
`!rss off` stops recording and hides the feed but keeps stored messages and tokens.

## Credentials

There are three kinds of secrets:

| Secret | Opens | Issued by |
| --- | --- | --- |
| **Root token** (`API_TOKEN` in `.env`) | the whole API including `/api/v1/admin/*` | you, in the environment |
| **API key** `mxk_…` | read-only `/api/v1/rooms*`, optionally limited to some rooms and to a validity period | bot admins (any rooms), room admins (their rooms only) |
| **Feed token** (in the feed URL) | one room's feed; a room can have several, one per reader | bot admins, room admins of that room |

- Bot admins and room admins create, list and revoke API keys and feed tokens in chat (above); the CLI and the HTTP admin API are for the operator who has the server or the root token. Every credential has a name, a creator and a creation time; revoking is immediate and keeps the record for audit.
- **A secret is shown once**, when it is created. Only its SHA-256 hash is stored, so a database leak does not reveal usable secrets and nobody can show it again. Lost secret: revoke it and issue a new one. The listings show a short hint (the beginning of the secret) to recognise a credential.
- **Room admin rights** are checked against the homeserver at the moment of the command: the user must be a joined member of the room with the required power level, so a user who left or was demoted loses them at once. If the bot cannot find out (for example it is not in the room), the answer is no. A room admin asking about a key or token of another room gets the same "no such key or token" as for a nonexistent ID.
- **Chat safety**: everything said in a shared room is visible to all its members, so `token create` and `key create` only work in a private chat (a room with just you and the bot) and otherwise tell you what to send there. `!rss on` and `!rss rotate` do show the feed URL in the room itself: the room members can read the room's messages anyway. The private chat should be end-to-end encrypted, otherwise the homeserver stores the secret in plain text.
- A feed URL created by an older version keeps working: it is migrated as a token named `legacy`.

## CLI

The management commands work on the database directly (needs `DATABASE_*` and `PUBLIC_BASE_URL`, no Matrix account) and can run while the bot is running. They read the database only; unlike the HTTP API they do not fetch missing history from Matrix.

```
matrixrss serve                                   run the bot and the HTTP server (default)
matrixrss rooms                                   list published rooms with feed URLs
matrixrss messages [-limit N] [-before CURSOR] [-deleted exclude|include|only] <room_id>
                                                  messages, oldest first (default 20)
matrixrss history <room_id> <event_id>            versions of an edited message
matrixrss rotate <room_id>                        revoke all feed tokens of a room, print a new feed URL
matrixrss disable <room_id>                       stop recording, hide the feed
matrixrss token create <room_id> <name>           issue another feed token (URL printed once)
matrixrss token list <room_id>                    list feed tokens
matrixrss token revoke <id>                       revoke a feed token
matrixrss key create [-rooms a,b] [-expires 30d] <name>   issue a read-only API key (secret printed once)
matrixrss key list                                list API keys
matrixrss key revoke <id>                         revoke an API key
matrixrss help
```

`messages` prints a `-before` hint on stderr for the next older page. Secrets go to stdout, notes to stderr, so `KEY=$(matrixrss key create grafana)` captures just the secret. Exit codes: 0 ok, 1 error, 2 wrong usage.

## Where messages come from

The database is a cache in front of the Matrix homeserver.

- **Live**: while a room is enabled, the bot records every message it receives through `/sync`. Edits and deletions arrive the same way.
- **On demand (API only)**: `GET .../messages` answers from the database. If the page holds fewer messages than `limit`, the bot asks the homeserver for older history (`/context`, then `/messages`), decrypts it, stores it and answers from the database again. Messages purged by `MESSAGE_RETENTION` are fetched again the same way. Later requests for the same range need no homeserver call. If the homeserver fails, the stored messages are returned.
- **Not fetched**: feeds (`/feeds/{token}`) and the CLI use the database only. Disabled rooms are never fetched. `deleted=only` does not trigger a fetch. Set `HISTORY_BACKFILL=false` to turn fetching off.
- **Limits**: only gaps *before the oldest stored message* are filled; a hole in the middle (for example messages missed while the bot was down for long) stays. Messages the bot has no keys for in encrypted rooms are skipped, and at most 5 homeserver requests of 100 events are made per API call. An edit is applied only if its original is stored or fetched in the same call.

## Kinds of messages

Every kind of message works in plain and in encrypted rooms alike.

| `kind` | What it is | Stored |
| --- | --- | --- |
| `text`, `notice`, `emote` | text; formatted text is kept as sanitized HTML | `body`, `formatted_body` |
| `image`, `video`, `audio`, `file` | attachments, voice messages are `audio` with `voice: true` | `body` (caption or name), `attachment` |
| `sticker` | a sticker | `attachment` |
| `location` | a shared place | `location.geo_uri` and a map link |
| `poll` | a poll start, as the question and numbered answers; votes and results are not tracked | `body` |
| `other` | a message type the bot does not know, with its text | `body` |
| `redacted` | a message that was already deleted when the bot first saw it | nothing |

Replies and threads are kept as `reply_to` and `thread_root` (event IDs); the quoted fallback text of old clients is removed from the reply. Reactions, votes, typing, read receipts and state changes are not messages and are not recorded.

**Formatted text** goes through an allow-list (the tags the Matrix specification recommends; no scripts, styles, images or `javascript:` links) before it is stored, so it is safe to show in a feed reader or a page.

**Attachments** are not copied to the bot's disk. On every download the bot fetches the file from the homeserver, decrypts it if the room is encrypted, checks its size against `MEDIA_MAX_SIZE` and streams it to the reader. So a deleted message, or a file the homeserver purged, is gone for readers too, and the bot stores no decrypted files. `attachment` describes the file (name, `mime_type`, `size`, `width`, `height`, `duration_ms`, `voice`) and has a `url` when the bot can serve it (not above the size limit, not turned off). Downloads accept `Range` requests and `ETag`. Pictures, audio and video are shown in place; everything else, SVG included, is sent as a download, always with `nosniff` and a sandbox policy, because the files come from other people.

## Edits and deletions

- **Edits** keep every version. The message holds the current text and `edited_at`; `GET .../messages/{eventID}/history` lists all versions, oldest first (the first is the original). Only the author's edits count, the newest edit wins.
- **Deletions** are kept as records: who deleted the message, when and why (`deleted` in the API). By default the message is hidden from listings and its **text is erased**; with `MESSAGE_KEEP_DELETED=true` the text stays readable with `deleted=include|only`. Messages that were already deleted when they were fetched appear as records of kind `redacted` without text.

## HTTP

### Feed: `GET /feeds/{token}`

No headers needed; the 256-bit random feed token in the URL is the credential. Unknown, revoked or disabled tokens give `404`.

- `format=rss` (default, `application/rss+xml`), `atom`, or `json` (JSON Feed).
- `limit=N`: newest N messages, default 50, max 200.
- One item per message, newest first, deleted messages left out; an edited message shows its current text. `title` = `Sender: first line of the text` (cut at 80 characters), `description` = the full text (plus the media URL on a new line for attachments), `guid` = Matrix event ID, `pubDate` = send time (UTC), `author` = display name, `link` = `https://matrix.to/#/{room}/{event}` permalink.
- The title of non-text items starts with a label: `[image]`, `[video]`, `[audio]`, `[voice message]`, `[file]`, `[sticker]`, `[location]`, `[poll]`. `description` is the plain text; a formatted message also has the sanitized HTML as `content:encoded`. Items with a file have an `<enclosure>` (type and size included) that points to `/feeds/{token}/media/{eventID}`: the same token downloads the file. Locations add the `geo:` URI and an OpenStreetMap link to the description.
- Caching: `ETag` and `Cache-Control: private, max-age=60`; send `If-None-Match` to get an empty `304` for an unchanged feed.

### API: `Authorization: Bearer <root token or API key>`

Missing, wrong, revoked or expired credentials give `401`. An API key used on a room outside its `rooms` gets `403`.

- `GET /api/v1/rooms` → `{"rooms":[{"room_id","title"}]}` for enabled rooms (an API key sees only its own rooms).
- `GET /api/v1/rooms/{roomID}/messages?limit=N&before=CURSOR&deleted=exclude|include|only` (room ID URL-encoded, e.g. `%21abc%3Aexample.org`):

  ```json
  {"messages":[{"seq":3,"event_id":"$3","room_id":"!abc:example.org","sender":"@alice:example.org",
                "sender_name":"Alice","kind":"text","body":"Rolling back?\nNo, all fine.",
                "sent_at":"2026-10-28T09:02:00Z","edited_at":"2026-10-28T09:05:00Z"}],
   "next_before":"1793178120000-3"}
  ```

  Newest first by send time. `kind` is one of the kinds above; `attachment`, `location`, `formatted_body`, `reply_to` and `thread_root` appear when they apply; `edited_at` for edited messages; `deleted: {at, by, reason}` for deleted ones (with `deleted=include|only`). `limit` defaults to 50 (max 200). `next_before` is an opaque cursor: pass it as `before` to get the next, older page, and stop when `messages` is empty. A room that never had a feed gives `404`; a disabled room stays readable (from the database). `seq` is only a local row number, not an order.
- `GET /api/v1/rooms/{roomID}/messages/{eventID}/history` → `{"event_id":"$3","revisions":[{"event_id","body","created_at"}]}`, oldest first. A message that was never edited has one revision. `404` for an unknown message.
- `GET /api/v1/rooms/{roomID}/messages/{eventID}/media` → the file of the message (the `attachment.url` of the listing). `404` for a message without a file, a deleted one, or a file the homeserver no longer has; `413` above `MEDIA_MAX_SIZE`; a key limited to other rooms gets `403`.
- `GET /healthz` → `200 ok`, no auth.

### Admin API: root token only (API keys get `403`)

- `POST /api/v1/admin/keys` with `{"name":"grafana","rooms":["!a:hs"],"expires_in":"30d"}` (`rooms` and `expires_in` optional; `expires_in` is a duration such as `720h` or `30d`) → `201` with the key and its `"secret":"mxk_…"` (only in this response). Unknown rooms, an empty or too long name and bad durations give `400`.
- `GET /api/v1/admin/keys` → `{"keys":[{id,name,hint,rooms,expires_at,created_at,created_by,revoked_at}]}`, never secrets.
- `DELETE /api/v1/admin/keys/{id}` → `204`; `404` for an unknown ID.
- `POST /api/v1/admin/rooms/{roomID}/tokens` with `{"name":"bob"}` → `201` with the token, its `secret` and the ready `feed_url` (only in this response); `404` if the room has no feed.
- `GET /api/v1/admin/rooms/{roomID}/tokens` → `{"tokens":[...]}`, never secrets.
- `DELETE /api/v1/admin/tokens/{id}` → `204`; `404` for an unknown ID.

```sh
curl -H "Authorization: Bearer $API_TOKEN" -d '{"name":"grafana","expires_in":"90d"}' https://rss.example.org/api/v1/admin/keys
```

## Build

Database drivers are selected with additive build tags: no tag means all, any tag means only the listed ones.

```sh
go build -tags goolm ./cmd/matrixrss                     # SQLite + PostgreSQL
go build -tags "goolm sqlite" ./cmd/matrixrss            # SQLite only
go build -tags "goolm postgres" ./cmd/matrixrss          # PostgreSQL only
docker build --build-arg DB_TAGS=sqlite -t matrixrss .
```

Run with `docker run --env-file .env -v matrixrss-data:/data -p 8080:8080 matrixrss`; CLI inside the container: `docker exec <container> matrixrss rooms`.

## Tests

`go test -tags goolm ./...` runs the unit tests. `e2e/` holds end-to-end tests against a local Conduit homeserver, with encrypted rooms; see [e2e/README.md](e2e/README.md).

## Layout

`domain` (entities) ← `usecase` (logic and repository ports) ← `adapter/*` (Matrix, HTTP, GORM) and `cli` ← `app` (wiring).

## Notes

- The bot downloads files with its own account (the authenticated media endpoint, with the older one as a fallback), so readers need no account on the homeserver. Each download holds the whole file in memory (at most `MEDIA_MAX_SIZE`), and at most 4 downloads run at once.
- Commands older than 5 minutes are ignored. A bot that starts without its sync position is handed the recent history of its rooms, and old commands in it (a `!rss rotate`, say) must not run again.
- Upgrading: messages stored by an earlier version keep their text but lose old media links (the column was replaced by the attachment description); `media_url` is no longer in the API.
- In encrypted rooms the bot only reads messages whose keys other members' clients share with its device.
- Fetching history from the homeserver was verified against a fake source and the database layer (SQLite and PostgreSQL), not yet against a real Matrix server.
