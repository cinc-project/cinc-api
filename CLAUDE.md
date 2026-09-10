# Notes for Claude Code

This is a Go client for the Chef Infra / CINC Server API. The package is
small, flat, and idiomatic — keep it that way.

## Project layout

- Public types and services live at the repo root, one file per service:
  `nodes.go` + `nodes_test.go`, `policies.go` + `policies_test.go`, etc.
- `internal/signing` implements the Chef v1.3 SHA-256 signed-header protocol.
- `internal/cinctest` is the test-only fake-server harness.
- `testdata/test_key.pem` is the shared fixture RSA key used by all tests.

Two procedures live in `.claude/skills/` instead of here, so they cost
nothing until they apply: **adding-a-service** (the shape every service
follows, and wiring it onto `*Client`) and **preparing-a-pr** (lint
commands and PR conventions).

## Testing

- Tests use `internal/cinctest`:
  ```go
  srv := cinctest.New(t)
  srv.Handle("GET /organizations/o/foo", cinctest.Route{Body: `{...}`})
  c := newTestClient(t, srv.Server)
  ```
  `Route.Assert` is a callback for inspecting the incoming request.
  The harness fails any test that issues an unsigned request, so
  signing is exercised on every call automatically.
- For requests outside the harness (path traversal, raw bookshelf
  uploads, etc.) set `srv.Server.Config.Handler` directly.
- Write tests **first**: red → implement → green. New files should land
  with 100% line coverage; the package as a whole sits at ~96% and
  should stay there or higher.
- Run `go test ./... -race -count=2` before committing. For coverage
  including the test-only `cinctest` package use
  `go test ./... -coverpkg=./... -coverprofile=...`.
- **Integration tests live in `integration/`, a *separate* Go module**
  (its own `go.mod`, so the cinc-zero test dependency never reaches
  consumers of this package — the root import stays zero-dependency).
  The root `go test ./...` does **not** run them. Run them with
  `cd integration && go test ./...` (~1s); they boot an in-memory
  cinc-zero server and exercise the real wire protocol end-to-end,
  unlike the `cinctest` fake the unit tests use. Run them when you
  touch the transport, signing, or cookbook-upload paths.

## What the test doubles do not cover

Both suites can be green while the wire contract is wrong. Known gaps, each
of which has hidden a real bug:

- **cinc-zero** returns only `all_files` on a cookbook GET (never the
  per-segment slices), accepts `run_list: null`, and populates both `name`
  and `groupname` on a group GET. A real Chef Server is stricter or shaped
  differently on all three.
- **cinctest** replays whatever body the test author wrote, so a fixture
  that encodes a wrong assumption about the server's response will happily
  confirm it forever.

So: when you change a wire shape, say explicitly what a *real* server
returns and how you know. "Tests pass" is not evidence of compatibility.
To probe actual behaviour, write a throwaway `zz_probe_test.go` in the
package, log what the client sends and what a fake server receives, then
delete it — that is how the escaping and manifest-dedupe bugs were pinned
down.

## Auth and transport gotchas

- The client signs every request with the v1.3 SHA-256 header protocol.
  The signed canonical path excludes any `?query` — see `transport.doOnce`;
  do not re-introduce it.
- **Pre-signed URLs (cookbook bookshelf, sandbox uploads) must NOT carry
  Chef signing headers** — `c.uploadFile` and `c.downloadFile` use raw
  `httpClient.Do` to enforce it. Any test that hits a bookshelf URL must
  assert `X-Ops-Authorization-1` is absent.
- **The signed canonical path must equal what goes on the wire**, i.e.
  `r.URL.EscapedPath()` — not `r.URL.Path`, which is already decoded and
  will hide a mismatch. `verifySignature` in `pathescape_test.go` re-checks
  the RSA signature server-side the way erchef does; use it whenever you
  touch path construction.
- Retries: GETs are retried on 5xx and on genuine wire failures, up to
  `WithMaxRetries(n)` (default 2), with exponential backoff from 100ms.
  Non-GET requests are never retried. Context cancellation/deadline never
  retries. Only errors wrapped in `transportErr` are retriable — if you add
  a new failure point in `doOnce` that happens on the wire, mark it, or it
  will never be retried; if it is client-side, do not, or it will be
  retried pointlessly.

## Encoding edge cases worth remembering

- `Group.Update` rewraps `Users/Clients/Groups` into the server's
  required `actors: {users, clients, groups}` shape.
- Any slice Chef validates as an array must serialize as `[]`, not
  `null`: `groups.go:nonNil`, `run_list` in `Node`/`Role.MarshalJSON`,
  and `normal.tags` in `SetTags`.
- A Go type assertion matches the **dynamic type exactly**, so
  `any(Attributes{}).(map[string]any)` is false even though the two share
  an underlying type. Attribute walking goes through `asAttributeMap`,
  which accepts both — do not reintroduce a bare assertion.
- `omitempty` does **not** elide nested struct values in Go's
  `encoding/json`. Use `omitzero` (Go 1.24+) or drop the tag when the
  field is a value-type struct.
- Cookbook upload is three-step: `POST /sandboxes` → file PUTs to the
  pre-signed URLs the server returned → manifest PUT. `uploadCookbook`
  in `cookbooks.go` is shared with cookbook artifacts.
- Search supports `WithStart`, `WithRows`, `WithPartial`. Passing
  `WithPartial` switches the underlying request from GET to POST with
  the projection map as the body — this is server-required, not a
  client choice.
