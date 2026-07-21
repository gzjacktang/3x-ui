# 3X-UI Lite architecture

This fork is a single-server Xray control panel optimized for a small VPS. It deliberately excludes the upstream dashboard and system history, nodes, hosts, client groups, built-in subscriptions, outbound subscription refresh, Telegram bot, SMTP notifications, and in-panel API documentation.

## Runtime shape

The release is one Go process plus the Xray process it manages. MTProto inbounds may also start `mtg-multi` sidecars. The Go binary embeds the production frontend and the two locale files.

The web process owns:

- login, sessions, CSRF, 2FA, and API tokens;
- local inbound and client CRUD;
- client traffic, quota, expiry, online, and IP-limit state;
- Xray configuration, lifecycle, manual outbounds, routing, DNS, and balancers;
- general, security, LDAP, WARP, and NordVPN settings;
- SQLite or PostgreSQL persistence;
- WebSocket updates for retained panel data.

There is no subscription listener, bot client, mail client, host or group service, node API, status-history sampler, or outbound subscription refresh loop.

## Request flow

Authenticated panel pages call `/panel/api/*` through `HttpUtil`. `APIController` applies session or bearer-token authentication and CSRF middleware, then delegates to the retained controllers:

- `internal/web/controller/inbound.go`
- `internal/web/controller/client.go`
- `internal/web/controller/server.go`
- `internal/web/controller/setting.go`
- `internal/web/controller/xray_setting.go`

Controllers validate and translate HTTP data. Business and persistence behavior lives under `internal/web/service/`. Xray process operations live under `internal/xray/` and the local runtime adapter under `internal/web/runtime/`.

`internal/web/controller/route_surface_test.go` protects the lite boundary by asserting that retained routes exist and removed route families are absent.

## Frontend

Vite builds two HTML entries into `internal/web/dist/`:

- `index.html`: authenticated React panel;
- `login.html`: login and 2FA.

React Router exposes only:

- `/inbounds`
- `/clients`
- `/outbound`
- `/routing`
- `/settings`
- `/xray`

The root redirects to `/inbounds`. Outbound and routing URLs open the relevant Xray settings view. The UI generates ordinary client share links and QR codes locally; it does not expose subscription URLs.

Frontend schemas under `frontend/src/schemas/` cover UI-owned data. Go entities are mirrored into `frontend/src/generated/` by `go run ./tools/openapigen`.

## Background work

`internal/web/web.go` schedules only runtime and maintenance work needed by retained features:

| Cadence | Work |
|---|---|
| 1 second | Check local Xray state |
| 30 seconds | Apply pending Xray restart |
| 5 seconds | Collect local Xray traffic |
| 10 seconds | Reconcile MTProto sidecars and scan client IP logs |
| 10 minutes | Prune Xray logs |
| Hourly | Refresh WARP IP and run hourly traffic resets |
| Daily/weekly/monthly | Scheduled client traffic resets |
| Configured | LDAP synchronization and optional memory release |

Removed feature jobs are not registered: node heartbeat and traffic sync, CPU or memory status sampling, event notifications, dashboard history, and outbound subscription refresh.

## Persistence compatibility

Database migrations and a small set of dormant fields remain so an existing upstream database can be opened without destructive conversion. In particular, legacy node identifiers and client `subId`/Telegram identifiers may still exist in stored rows or compatibility structs. They have no panel route, API surface, scheduled work, or active Xray configuration path in the lite build.

This distinction is intentional: preserve upgrade readability while removing runtime ownership and memory cost.

## Languages

Only `internal/web/translation/en-US.json` and `zh-CN.json` are embedded and offered by the frontend. Any new translation key must be added to both files.

## Verification

For changes that cross the Go/frontend contract:

```sh
go run ./tools/openapigen
cd frontend && npm run typecheck && npm run lint && npm run test && npm run build
cd .. && go test -p 1 ./... && go build ./...
```

The Storybook browser project additionally requires a locally installed Playwright Chromium binary.
