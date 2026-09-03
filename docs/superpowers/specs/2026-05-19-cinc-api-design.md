# cinc-api — Go client for the Chef/CINC Server API

**Date:** 2026-05-19
**Module:** `github.com/cinc-project/cinc-api`
**Root package:** `cinc`

## Goal

A modern, idiomatic Go client for the Chef Infra / CINC Server API. Clean-slate
API (not bound to `go-chef/chef` conventions): `context.Context` everywhere,
generics for transport, structured errors, functional options. Full API parity
is a long-term goal; the first milestone covers the core config-management
surface plus cookbooks.

## Decisions

- **Motivation:** modern idiomatic rewrite, full parity over time.
- **API shape:** service-grouped (go-github style) — one `Client`, services hang
  off it (`client.Nodes`, `client.Cookbooks`, …), one file per service.
- **Auth:** v1.3 signed-header protocol only (SHA-256). No legacy 1.0/1.1.
- **v0.1 scope:** core config-management resources + cookbooks.
- **Architecture:** single module, layered packages; signer unexported under
  `internal/signing`; private generic `crud[T]` helper dedupes CRUD boilerplate.

## Package layout

```
cinc-api/
  client.go            Client, NewClient, Config
  options.go           functional options (WithHTTPClient, WithUserAgent…)
  key.go               ParseKey / LoadKeyFile  (RSA private key)
  transport.go         request build, do[T], doRaw, bounded retry
  response.go          Response wrapper (raw *http.Response + metadata)
  errors.go            ErrorResponse, sentinel errors
  attributes.go        Attributes — typed wrapper over the free-form attr tree
  crud.go              internal generic crud[T] helper
  nodes.go roles.go environments.go databags.go clients.go
  search.go
  cookbooks.go cookbook_artifacts.go sandboxes.go
  internal/signing/    v1.3 SHA-256 signer (canonical request + header chunking)
  internal/cinctest/   in-process test server harness
  testdata/            JSON fixtures, sample keys, sample cookbooks
```

Module path `github.com/cinc-project/cinc-api`; Go 1.26.

## Client construction

Required fields in a `Config` struct (cannot be forgotten); everything optional
behind functional options.

```go
key, err := cinc.LoadKeyFile("/etc/chef/client.pem")

client, err := cinc.NewClient(cinc.Config{
    ServerURL:  "https://chef.example.com",
    Org:        "myorg",
    ClientName: "node-or-user-name",
    Key:        key,
},
    cinc.WithHTTPClient(customHC),
    cinc.WithUserAgent("mondoo-scanner/1.0"),
    cinc.WithSkipTLSVerify(false),
)
```

`ServerURL` and `Org` are kept separate so the client can address org-scoped
endpoints (`/organizations/myorg/nodes`) and, later, root-scoped
account-management endpoints without reparsing URLs. The `Client` is safe for
concurrent use.

## Transport & signing

**Signing** (`internal/signing`, unexported): the v1.3 SHA-256 protocol. The
signer is a pure function of `(request, key, clock)` — no I/O. It produces the
RSA signature and chunks it across `X-Ops-Authorization-1..N` headers alongside
`X-Ops-Sign`, `X-Ops-Timestamp`, `X-Ops-UserId`, `X-Ops-Content-Hash`.

**Transport:** one generic chokepoint —

```go
func do[T any](ctx context.Context, c *Client, method, path string,
                body any) (T, *Response, error)
```

builds → signs → sends → decodes JSON into `T`, or maps non-2xx to a typed
error. `doRaw` returns an `io.ReadCloser` for cookbook file downloads. Idempotent
GETs get bounded retry on 5xx/network errors; writes never auto-retry. The
`*http.Client` and a clock are injectable for deterministic tests.

## Error handling

Non-2xx becomes `*ErrorResponse` carrying status code, the Chef `error` message
array, and request context (method, path). Sentinel errors enable `errors.Is`:

```go
errors.Is(err, cinc.ErrNotFound)     // 404
errors.Is(err, cinc.ErrConflict)     // 409 — object already exists
errors.Is(err, cinc.ErrForbidden)    // 403 — ACL denial
errors.Is(err, cinc.ErrUnauthorized) // 401 — bad key / clock skew
```

A 401 is inspected for clock skew (the most common signing failure) and surfaces
a clear message.

## Resource model

Typed structs for known fields; an `Attributes` type wrapping `map[string]any`
for the free-form attribute bags.

```go
type Node struct {
    Name        string     `json:"name"`
    Environment string     `json:"chef_environment"`
    RunList     []string   `json:"run_list"`
    Normal      Attributes `json:"normal"`
    Default     Attributes `json:"default"`
    Override    Attributes `json:"override"`
    Automatic   Attributes `json:"automatic"`
    PolicyName  string     `json:"policy_name,omitempty"`
    PolicyGroup string     `json:"policy_group,omitempty"`
}
```

`Attributes` adds deep access — `Dig(path...)`, `GetString(path...)` — and
round-trips JSON losslessly so unknown server fields survive a
read-modify-write.

## Services

Uniform per resource, backed by the internal `crud[T]` helper:

```go
node, resp, err := client.Nodes.Get(ctx, "web01")
node.RunList = append(node.RunList, "recipe[nginx]")
node, resp, err = client.Nodes.Update(ctx, node)
created, resp, err := client.Nodes.Create(ctx, &cinc.Node{Name: "web02"})
err = client.Nodes.Delete(ctx, "web02")
names, resp, err := client.Nodes.List(ctx)   // name → URL index
```

Roles, environments, clients, data bags follow the identical pattern. Data bags
nest: `client.DataBags.Items("creds")`. `List` returns Chef's actual response —
a name→URL map — not a faked full-object list.

## Search

Dedicated service (not `crud`):

```go
res, resp, err := client.Search.Query(ctx, "node",
    "chef_environment:production",
    cinc.WithStart(0),
    cinc.WithRows(100),
    cinc.WithPartial(map[string][]string{
        "fqdn": {"automatic", "fqdn"},
        "ip":   {"automatic", "ipaddress"},
    }),
)
// res.Total, res.Rows ([]json.RawMessage — caller decodes)
```

`SearchAll` transparently pages until exhausted. Rows stay `json.RawMessage`
because a query can target any index.

## Cookbooks

Upload is a three-step protocol hidden behind one call:

1. Compute MD5 checksums of every file.
2. `POST /sandboxes` with the checksum set → server replies which are needed.
3. `PUT` each needed file to its signed upload URL, then `PUT` the version
   manifest.

```go
err := client.Cookbooks.Upload(ctx, cookbook)
cb, _, err := client.Cookbooks.Get(ctx, "nginx", "1.2.0")
err = client.Cookbooks.Download(ctx, "nginx", "1.2.0", destDir)
list, _, err := client.Cookbooks.List(ctx)        // name → versions
err = client.Cookbooks.Delete(ctx, "nginx", "1.2.0")
```

`cookbook_artifacts` (Policyfile-mode, content-addressed) share the
sandbox/checksum machinery via `sandboxes.go`, exposed as a parallel
`client.CookbookArtifacts` service. Reading a cookbook off disk
(`cookbookFromDir`) is a separate, independently testable concern from the
network protocol.

## Testing strategy

First-class. Three layers, all hermetic (no live server, no network):

- **Signing unit tests** — `internal/signing` exhaustively covered against
  known-good vectors. Canonical-request construction, content hashing, header
  chunking, timestamp formatting tested as isolated pure functions. At least one
  vector is cross-checked against the Ruby `mixlib-authentication` reference
  output to confirm wire-correctness.
- **Service unit tests** — every service method tested against an `httptest`
  server from the `internal/cinctest` harness. The harness asserts request
  method, path, presence of signed headers, and body JSON, returning canned
  fixtures including real Chef error bodies. Each method covers: happy path,
  404→`ErrNotFound`, 409→`ErrConflict`, 403→`ErrForbidden`, malformed JSON, and
  `context` cancellation.
- **Protocol tests** — the cookbook sandbox/upload flow gets a stateful fake
  server walking all three steps, plus a round-trip:
  `cookbookFromDir` → `Upload` → `Download` → byte-compare.

Supporting choices: injectable clock and `*http.Client` make time- and
network-dependent paths deterministic; table-driven tests throughout; fixtures
in `testdata/`; `Attributes` JSON round-trip is property-style tested so
read-modify-write provably never drops fields. Goal: every error branch and edge
case exercised, with `-race` in CI.

## Milestones

- **v0.1** — signing, transport, errors, `Client`/`Config`, `Attributes`;
  services for nodes, roles, environments, data bags, clients, search,
  cookbooks + cookbook_artifacts. Full test suite. CI (build, vet, test, race).
- **Later (parity)** — users, organizations/account-management, ACLs,
  groups/containers, keys, policies/policy-groups, principals, status, universe,
  license.

## Out of scope (v0.1)

- Legacy signing protocols (1.0/1.1).
- Account-management / root-scoped endpoints.
- Knife-style config-file (`knife.rb`/`config.rb`) parsing.
- A CLI — this is a library only.
