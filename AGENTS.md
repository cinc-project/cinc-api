# Notes for coding agents

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
  (its own `go.mod`, so the cinc-server-ng test dependency never reaches
  consumers of this package — the root import stays zero-dependency).
  The root `go test ./...` does **not** run them. Run them with
  `cd integration && go test ./...` (~2s); they exercise the real wire
  protocol end-to-end, unlike the `cinctest` fake the unit tests use. Run
  them when you touch the transport, signing, or cookbook-upload paths.
  - `integration/suite` holds every test, written once against a
    `suite.Target`. Add new integration tests here, to the `cases` table.
    Each case gives every object it creates a `uniqueName` and a
    `cleanup`, and runs in parallel.
  - `integration/cincserverng` boots one in-memory cinc-server-ng and
    runs the suite against it. A test that cinc-server-ng cannot pass is
    listed in its `Target.Gaps` with the upstream issue URL, never
    deleted or special-cased; `suite.Run` fails on a gap that names no
    test or gives no reason. Remove the gap once the issue is fixed.
  - `integration/cincservererlang` runs the same suite against the CINC
    Server Erlang stack that `integration/terraform` brings up in AWS.
    Run it with `integration/run-cinc-server-erlang.sh` (needs AWS
    credentials; ~10 min, ~$0.17/h; see `integration/README.md`). Without
    a stack it skips, so CI only compiles it. **Run it before merging any
    change to a request or response shape**: it is the only suite that
    talks to erchef itself.

## What the test doubles do not cover

Both suites can be green while the wire contract is wrong. Known gaps, each
of which has hidden a real bug (all re-checked against cinc-server-ng v0.14.0;
file new ones at cinc-project/cinc-server-ng):

- **cinc-server-ng** returns only `all_files` on a cookbook GET (never the
  per-segment slices), accepts `run_list: null` on a node, and populates
  both `name` and `groupname` on a group GET. A real Chef Server is stricter or shaped
  differently on all three.
- **cinc-server-ng** stores node and role run lists verbatim. erchef
  normalizes them on every save (bare `nginx` becomes `recipe[nginx]`, then
  exact duplicates are dropped; `chef_object_base:normalize_run_list`), so a
  run list read back from a real server can differ from the one sent.
  `NormalizeRunList` reproduces erchef's result client-side.
- **cinc-server-ng** generates a client key on `POST /clients` even without
  `create_key`. Real erchef under API v1 creates a keyless client (no
  `chef_key` in the response, no `default` key) unless `create_key: true`
  or `public_key` is sent.
- **cinc-server-ng** accepts a cookbook manifest with no `metadata` block,
  with bare file names (`default.rb` instead of `recipes/default.rb`), and
  with `all_files` at server API version 1. erchef rejects the first and
  third with a 400 (`metadata.version` must equal the URL version;
  `all_files` is only valid from API version 2, which is why the manifest
  PUT asks for 2), and chef-client misfiles every entry under the second.
- **Group PUT** differs too. erchef answers with the request body echoed
  (members nested under `actors`, unresolvable names still in it) and keeps
  any `actors` kind the body omits; cinc-server-ng answers with the stored
  GET-shaped group (unknown names already dropped) and clears an omitted
  kind. Both silently drop a name that resolves to nothing, which is why
  `Groups.AddMembers` reads the group back. The flat `actors` array on a GET
  is clients+users on erchef but every member on cinc-server-ng.
- **cinc-server-ng** lets a non-superuser take the `admins` group off an
  object's `grant` ACE; erchef refuses that with a 403 ("Admin group cannot
  be removed from the Grant ACE"), not a 400.
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
- **Signed requests never follow redirects.** `NewClient` copies the
  `http.Client` (default or `WithHTTPClient`) and sets `CheckRedirect` to
  `refuseRedirect` on the copy, never on the caller's; `doOnce` turns the
  3xx into an `*ErrorResponse` naming the `Location` (`redirectError`).
  Following one would forward the `X-Ops-*` headers to the new host.
  `transferClient` is copied before that and keeps the caller's policy, so
  bookshelf/S3 transfers still follow redirects.
- Retries: GETs are retried on 5xx and on genuine wire failures, up to
  `WithMaxRetries(n)` (default 2), with exponential backoff from 100ms.
  Non-GET requests are retried only on a `503` (`retryable`): the server
  refused them unprocessed (erchef does this when its key-generation pool
  runs dry under parallel user/client creation), while a 500/502/504 or a
  wire failure may follow a request that was applied. A 503's `Retry-After`
  lengthens the wait, capped at `maxRetryAfter`. Every retry goes back
  through `doOnce`, so it resends the same body and is re-signed with a
  fresh timestamp. Context cancellation/deadline never retries. Only errors wrapped in `transportErr` are retriable — if you add
  a new failure point in `doOnce` that happens on the wire, mark it, or it
  will never be retried; if it is client-side, do not, or it will be
  retried pointlessly. A `transportErr` that is a failed certificate
  verification, or a plain-HTTP server on an `https://` URL, is permanent
  (`isPermanentTLSError`) and is not retried either.
- Bookshelf transfers (`uploadFile`, `downloadFile`) are the exception to
  "non-GET never retried": they go through `doTransfer`, which applies
  the same `shouldRetry`/`backoff` policy to the pre-signed PUT as well,
  because it is idempotent (content-addressed by checksum). They use
  `transferClient` (timeout `WithTransferTimeout`, default 10m), not
  the API client's 30s timeout.

## Encoding edge cases worth remembering

- `Group.Update` rewraps `Users/Clients/Groups` into the server's
  required `actors: {users, clients, groups}` shape, and
  `Group.UnmarshalJSON` reads that shape back (an `actors` object), so a
  PUT-shaped group file does not decode to an empty group.
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
- Search supports `WithStart`, `WithRows`, `WithPartial`,
  `WithPartialPaths`. A partial projection switches the underlying request
  from GET to POST with the projection map as the body — this is
  server-required, not a client choice. Partial rows come back as
  `{"url", "data"}` and a full data bag search wraps each item in a
  `Chef::DataBagItem` envelope (`raw_data`); `UnwrapSearchRow` owns both
  shapes (erchef `chef_wm_search:make_bulk_get_fun`,
  `chef_data_bag_item:wrap_item`), so callers never match on them.
