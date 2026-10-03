<div align="center">

# NetSpace · Backend

**REST API and real-time WebSocket server for NetSpace, a location-based social app for cafés.**

![Go](https://img.shields.io/badge/Go_1.26-00ADD8?logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?logo=postgresql&logoColor=white)
![chi](https://img.shields.io/badge/router-chi%2Fv5-333)
![WebSocket](https://img.shields.io/badge/gorilla%2Fwebsocket-realtime-6E56CF)
![JWT](https://img.shields.io/badge/auth-JWT-000000?logo=jsonwebtokens&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)

<img src="docs/screenshots/hero.png" alt="NetSpace mobile screens powered by this backend" width="100%">

[Web client](https://github.com/Kristantowinata/netspace-frontend) · **Go backend (this repo)**

</div>

---

## What it does

Visitors scan a café's QR code, pass a GPS geofence check in the browser, and check in with just a name and some interests. This service:

- issues **session JWTs** at check-in and **admin JWTs** that are bound to a single venue;
- runs one **real-time hub per café** for presence, the public room, direct messages, group chats, typing indicators, read receipts and notifications;
- stores everything in **PostgreSQL** and cleans up after itself: a visitor's chats are purged on logout, public-room messages expire after 24 hours, and stale "online" flags are reset on boot;
- serves the **admin API**: analytics, active users, force-logout, a venue on/off switch and public-chat moderation.

Screenshots of every screen are in the [frontend README](https://github.com/Kristantowinata/netspace-frontend#screenshots).

| Admin analytics | Public-chat moderation |
|:---:|:---:|
| <img src="docs/screenshots/11-admin-analytics.png" alt="Admin analytics served by /api/admin endpoints"> | <img src="docs/screenshots/14-admin-public-chat.png" alt="Admin moderating the public room over WebSocket"> |

## Architecture

```mermaid
flowchart TB
    Client["Browser<br/>(Next.js client)"]
    subgraph Go["Go service"]
        Router["chi router<br/>CORS · body limit · JWT auth"]
        Handlers["REST handlers"]
        WS["/ws upgrade"]
        Manager["Manager"]
        HubA["Hub · kopiloka"]
        HubB["Hub · koktong"]
        Repo["Repository (database/sql)"]
    end
    DB[("PostgreSQL")]

    Client -->|REST| Router --> Handlers --> Repo
    Client <-->|WebSocket| WS --> Manager
    Manager --> HubA & HubB
    HubA & HubB --> Repo --> DB
```

- **One `Hub` per venue.** Each hub's `run()` goroutine is the only owner of its in-memory state (connected clients, groups, blocks). Every change goes through channels into that loop, so the hub needs no locks.
- **One `Client` per socket**, each with a read pump and a write pump. A slow client is dropped instead of being allowed to stall a broadcast.
- **Persistence happens behind the hub.** Messages, groups and notifications are written through the `db.Repository`. Reconnecting clients get their history over REST.
- **Run a single instance.** Hub state lives in process memory, so several replicas would split a café's visitors across hubs.

```
main.go                entrypoint
app/env.go             wiring: config, DB, auth, chat manager
handler/               routes (server.go), REST handlers, WebSocket upgrade
chat/                  realtime core: manager, hub, client, chat/group/typing/read/block logic
db/                    SQL repository, one file per domain
model/                 domain types
api/                   DTOs and WebSocket event types (the wire format)
auth/                  JWT, token blacklist, password hashing
middleware/            CORS, request-size limit, user/admin auth
netspace.sql           schema, idempotent migrations, demo seed
migrations/            standalone migration scripts for existing databases
```

## API

### REST

| Method | Path | Auth | Purpose |
|---|---|:---:|---|
| `POST` | `/api/sessions/check-in` | none | Create a visitor session. Returns `userId` and a JWT. |
| `GET` | `/api/locations/{slug}` | none | Venue info, including geofence center and radius |
| `GET` | `/api/locations/{slug}/users` | user | Who is at the venue right now |
| `GET` | `/api/locations/{slug}/public-messages` | user | Recent public-room history |
| `GET` | `/api/chats` | user | Conversation list (DMs and groups) |
| `GET` | `/api/chats/{userId}/messages` | user | DM history |
| `GET` | `/api/groups/{groupId}/messages` | user | Group history (members only) |
| `GET` | `/api/notifications` | user | Notifications |
| `GET` | `/api/sessions/logout` | user | Revoke the token and purge the visitor's chat data |
| `POST` | `/api/admin/login` | none | Admin login. Returns an admin JWT bound to the venue. |
| `GET` | `/api/admin/dashboard/stats` | admin | Check-ins, users online, conversations (today vs yesterday) |
| `GET` | `/api/admin/analytics/hourly` · `/interests` | admin | Check-ins per hour (WIB) and top interests |
| `GET` · `PUT` | `/api/admin/locations/{slug}` | admin | Venue details · open/close check-in |
| `GET` | `/api/admin/locations/{slug}/users` | admin | Active visitors |
| `GET` · `DELETE` | `/api/admin/locations/{slug}/public-messages` | admin | Read or clear the public room |
| `POST` | `/api/admin/users/{userId}/kick` | admin | Force-logout a visitor at the admin's own venue |

### WebSocket

Connect to `GET /ws?token=<JWT>&locationSlug=<slug>`. Every frame is JSON shaped like `{ "event": string, "data": object }`.

| Direction | Events |
|---|---|
| Client → server | `send_message`, `send_public_message`, `typing_start`, `typing_stop`, `mark_read`, `create_group`, `invite_to_group`, `accept_group_invite`, `rename_group`, `leave_group`, `block_user`, `dismiss_notification`, `ping`, … |
| Server → client | `user_joined`, `user_left`, `new_message`, `new_public_message`, `user_typing`, `messages_read`, `group_created`, `member_joined`, `group_renamed`, `new_notification`, `force_logout`, … |

The full payload definitions are in [`api/ws_events.go`](api/ws_events.go), mirrored in the frontend's `lib/wsTypes.ts`.

## Run it locally

You need **Go 1.26+** and **PostgreSQL 14+**.

```bash
git clone https://github.com/Kristantowinata/netspace-backend.git
cd netspace-backend

createdb netspace
psql "postgres://USER:PASSWORD@localhost:5432/netspace?sslmode=disable" -f netspace.sql

cp .env.example .env     # then fill in the values
go run .                 # → "Server is listening on port 8080"
```

Run the tests with `go test ./...`. To build a binary, use `go build -o netspace .`.

### Environment variables

| Variable | Required | Description |
|---|:---:|---|
| `DB_CONN_STRING` | ✅ | PostgreSQL DSN, e.g. `postgres://user:pass@host:5432/netspace?sslmode=disable`. Use `sslmode=require` for managed databases. |
| `JWT_SECRET_KEY` | ✅ | Signs every JWT. **The server refuses to start without it** and warns if it's shorter than 32 characters. Generate one with `openssl rand -base64 48`. |
| `PORT` | | Listen port. Most hosts inject it. Defaults to `8080`. |

A local `.env` is loaded if present. In production, variables come from the platform. `.env` is git-ignored and excluded from the Docker image.

## Database and seed

`netspace.sql` creates the schema, runs idempotent migrations (`ADD COLUMN IF NOT EXISTS`), and seeds:

- **3 demo cafés**: `kopiloka`, `koktong`, `kopi-braga`, each with geofence coordinates;
- **3 demo admins**: usernames `kopiloka`, `koktong`, `kopibraga`, demo password `admin123`, stored as a **bcrypt hash**. Log in at `/<slug>/admin/login` on the frontend. **Replace these before any real deployment.**

<details>
<summary><b>Changing a venue's geofence</b></summary>

Each row in `Locations` has `latitude`, `longitude` and `geofenceRadius` (meters). The client only lets a visitor in when their GPS fix is within that radius.

```sql
UPDATE Locations
SET latitude = -6.200754, longitude = 106.783913, geofenceRadius = 40
WHERE slug = 'kopiloka';
```

For a real café, `30–50 m` works well because phone GPS is usually accurate to 10–30 m. The frontend also has a global testing override; see its README.

</details>

## Security

The repo is public, so these protections are built in:

| Area | What's in place |
|---|---|
| **Authentication** | HS256 JWTs with the signing method enforced on verify and an 8-hour expiry. User and admin tokens are kept apart by an `actorType` claim, and admin tokens are bound to one venue. |
| **Secrets** | Only read from the environment. The server won't start without `JWT_SECRET_KEY`, because an empty key would make tokens forgeable. No secrets in git history. |
| **Admin passwords** | Stored as **bcrypt** hashes. Rows from older plaintext seeds are compared in constant time and upgraded to bcrypt on the next successful login. |
| **Authorization** | Admin endpoints check that the requested venue matches the token's venue, and force-logout only works on visitors at that venue. Only group members can read a group's history or post to it. Outsiders get the same 404 as for a missing group. |
| **Session revocation** | Logout blacklists the token for both REST and new WebSocket connections, then purges that visitor's chat data. |
| **Abuse limits** | Request bodies capped at 1 MB, WebSocket frames at 64 KB, a header-read timeout against slow-loris clients, and the token blacklist is safe for concurrent use. |
| **SQL** | Every query uses bound parameters (`$1`, `$2`, …). |

**Before using it in production:**
- **Lock down CORS and the WebSocket origin check.** Both currently allow any origin. Because auth uses bearer tokens rather than cookies, this doesn't enable CSRF, but they should still be restricted to your frontend domain.
- **Add rate limiting** to login, check-in and message sending.
- **Scope analytics by venue if you add more cafés.** `dashboard/stats`, `analytics/hourly` and `analytics/interests` currently aggregate across all venues.
- **Persist revoked tokens if you need them to survive a restart.** The blacklist is in memory, and tokens expire after 8 hours anyway.

## Docker and deployment

```bash
docker build -t netspace-backend .
docker run -p 8080:8080 \
  -e DB_CONN_STRING="postgres://user:pass@host:5432/netspace?sslmode=require" \
  -e JWT_SECRET_KEY="$(openssl rand -base64 48)" \
  netspace-backend
```

This is a multi-stage build that produces a static binary on Alpine and reads `$PORT`. On Railway or Render:

1. Deploy the repo; the `Dockerfile` is detected automatically.
2. Add PostgreSQL and set `DB_CONN_STRING` (on Railway: `${{Postgres.DATABASE_URL}}`).
3. Set a strong `JWT_SECRET_KEY`.
4. Run `netspace.sql` once against the database.
5. Use the public `https://…` URL as the frontend's `NEXT_PUBLIC_API_BASE_URL`. The WebSocket upgrades to `wss://` automatically.
6. Keep it at **one replica** (see Architecture).

## Team

Built as a Software Engineering team project at BINUS University (semester 4).

| Member | Backend focus |
|---|---|
| [@latoiste](https://github.com/latoiste) | Core architecture: WebSocket hub, chat, REST API, JWT auth |
| **Kristanto Winata** ([@Kristantowinata](https://github.com/Kristantowinata)) | Group-chat overhaul, read receipts, geofence data, public-room history, admin public-chat moderation, WIB analytics, deployment (Docker, `PORT`), security hardening. Also built the entire [web client](https://github.com/Kristantowinata/netspace-frontend). |
| Kevin Nathanael Limarga | Admin API, analytics queries, database schema |

## License

Shared for portfolio and educational purposes. All rights reserved by the authors.
