---
name: adding-a-service
description: Use when adding a new service, endpoint family, or wire type to the cinc-api Go client, or when wiring a new resource onto *Client
---

# Adding a service

Every service in this package follows one shape. Match it rather than
inventing a new one — the package is small, flat, and idiomatic.

## Steps

1. Create `foo.go` with:
   - The wire types (struct fields use JSON tags; prefer `omitempty`).
   - A `FooService struct{ client *Client }` and its methods.
   - Standard signatures: reads return `(value, *Response, error)`;
     deletes return `(*Response, error)`. Use `ptrOrNil(v, err)` to
     convert a value to a `*T` that is `nil` when `err != nil`.
     A create returns `(*Response, error)` too when the server answers
     with `{"uri":...}` — check what the endpoint actually returns before
     promising a value the caller will never get.
   - Use the generic helper `do[T](ctx, c, method, path, body)` for
     every request. It signs, retries idempotent calls, decodes JSON,
     and maps non-2xx responses to `*ErrorResponse`.
   - If the resource has plain Get/Create/Update/Delete/List, the
     `crud[T]` helper in `crud.go` saves boilerplate (see `nodes.go`).
2. Wire the service onto `*Client` in `client.go` — declare the field on
   the struct and assign it in `NewClient`. Add it to the
   `TestNewClient_WiresAllServices` check.
3. Use `c.orgPath(p)` for org-scoped paths (`/organizations/<org>/...`).
   Top-level endpoints (e.g. `/_status`, `/license`, `/users`) take the
   absolute path directly — do **not** call `orgPath`.
   Wrap every caller-supplied identifier in `esc()` as you interpolate it
   (`orgPath("/nodes/" + esc(name))`). It is the identity function for
   legal Chef names; without it a name can break the signature or walk
   into another collection. (`esc` arrives with the path-escaping fix; if
   it is not in the tree yet that change is still in review — escape the
   segment rather than concatenating raw.)
4. Update the `README.md` Status table in the same PR.

## Testing the new service

Write tests **first**: red → implement → green. New files should land with
100% line coverage. See the Testing section of `CLAUDE.md` for the
`cinctest` harness and the commands to run.
