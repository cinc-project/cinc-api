# cinc-api

A modern, idiomatic Go client for the Chef Infra / CINC Server API.

## Install

    go get github.com/cinc-project/cinc-api

## Usage

    key, _ := cinc.LoadKeyFile("/etc/chef/client.pem")
    c, err := cinc.NewClient(cinc.Config{
        ServerURL:  "https://chef.example.com",
        Org:        "myorg",
        ClientName: "node1",
        Key:        key,
    })
    node, _, err := c.Nodes.Get(context.Background(), "web01")

## Status

Authentication uses the Chef v1.3 SHA-256 signed-header protocol. The
following endpoint families are implemented:

| Service              | Path                                  | Methods                                                              |
| -------------------- | ------------------------------------- | -------------------------------------------------------------------- |
| `c.ACLs`             | `/<object>/<name>/_acl`               | Get / SetPermission; org-level (`GetOrg`/`SetOrgPermission`) and global-user (`GetUser`/`SetUserPermission`) |
| `c.Associations`     | `/organizations/O/users`, `/association_requests`, `/users/U/...` | Members (ListMembers/GetMember/AddMember/RemoveMember), org invites (ListInvites/Invite/RescindInvite), user invites (ListUserInvites/UserInviteCount/RespondInvite) and ListUserOrgs |
| `c.Clients`          | `/clients`                            | List / Get / Create / Update / Delete / Reregister                   |
| `c.Containers`       | `/containers`                         | List / Get / Create / Delete                                         |
| `c.Cookbooks`        | `/cookbooks`                          | List / GetVersions (one cookbook, `num_versions`) / Get (with metadata) / Delete / Upload (sandbox flow) / Download / ListLatest / ListRecipes |
| `c.CookbookArtifacts`| `/cookbook_artifacts`                 | List / GetVersions (one artifact) / Get (with metadata) / Delete / Upload |
| `c.DataBags`         | `/data`                               | List / Create / Delete; per-bag Items handle for CRUD; `DataBagItem.Encrypt`/`Decrypt`/`IsEncrypted` for the Chef encrypted-data-bag format (writes v3 AES-256-GCM, reads v1/v2/v3) |
| `c.Environments`     | `/environments`                       | List / Get / Create / Update / Delete / ListCookbooks / GetCookbook / CookbookVersions / ListNodes / ListRecipes / RoleRunList |
| `c.Groups`           | `/groups`                             | List / Get / Create / Update / Delete                                |
| `c.Keys`             | `/users/U/keys`, `/clients/C/keys`    | `User(name)` / `Client(name)` → List / Get / Create / Update / Delete |
| `c.License`          | `/license`                            | Get (node-license usage)                                             |
| `c.Nodes`            | `/nodes`                              | List / Get / Create / Update / Delete                                |
| `c.Orgs`             | `/organizations` (top-level)          | List / Get / Create / Update / Delete                                |
| `c.Policies`         | `/policies`                           | List / Get / Delete / GetRevision / CreateRevision / DeleteRevision / PushRevision |
| `c.PolicyGroups`     | `/policy_groups`                      | List / Get / Delete / GetPolicy / PutPolicy / DeletePolicy           |
| `c.Principals`       | `/principals/<name>`                  | Get (public key(s) + type for a user/client)                        |
| `c.RequiredRecipe`   | `/required_recipe`                    | Get (returns Ruby text/plain)                                        |
| `c.Roles`            | `/roles`                              | List / Get / Create / Update / Delete / Environments / EnvironmentRunList |
| `c.Search`           | `/search/INDEX`                       | `Query` (with `WithStart`/`WithRows`/`WithPartial`), `SearchAll`, `Indexes` |
| `c.Stats`            | `/_stats` (top-level, Basic auth)     | Get (Erchef/PostgreSQL/VM metrics; not Chef-signed)                 |
| `c.Status`           | `/_status`                            | Get (server health + keygen pool)                                    |
| `c.Universe`         | `/universe` (org + top-level)         | Get / GetGlobal (known cookbooks + dependencies)                    |
| `c.Users`            | `/users` (top-level)                  | List / Get / Create / Update / Delete / Authenticate                 |

Configurable via options: `WithHTTPClient`, `WithUserAgent`,
`WithChefVersion`, `WithSkipTLSVerify`, `WithMaxRetries`. Idempotent GETs
are retried on 5xx and network errors.

### Helpers

Standalone helpers for working with Chef/CINC identities and the node object
model, so callers don't re-encode server conventions:

- `ParseServerURL(raw)` — split `https://host/organizations/<org>` into the
  base server URL and org (the inverse of `NewClient`'s `ServerURL`/`Org`).
- `GenerateKeyPair()` — mint a 2048-bit RSA key pair as PEM (the generation
  counterpart to `ParseKey`/`LoadKeyFile`).
- `Node` accessors — `Tags`/`SetTags`/`AddTags`/`RemoveTags` (stored at
  `normal.tags`), `AddRunListItems`/`RemoveRunListItems`, and
  `Attribute`/`AttributeString` (precedence-aware lookup, dotted paths).
- `Clients.Reregister(name)` — regenerate a client's `default` key and return
  the new private key.
- `ParsePolicyfileLock(data)` / `LoadPolicyfileLock(path)` — parse a
  `Policyfile.lock.json` into a `PolicyRevision`.
- `CookbookLock` accessors — `Origin()` (classify a lock's `source_options` as
  `path`/`artifactserver`/`git`/`chef_server` and return its location) and
  `PinnedVersion()` (the `source_options` version, falling back to the lock's
  top-level version).
- `DataBagItem.Encrypt(secret)` / `Decrypt(secret)` / `IsEncrypted()` — the
  Chef encrypted-data-bag-item codec. `Encrypt` boxes every value except `id`
  in a version-3 (AES-256-GCM) wrapper; `Decrypt` reads versions 1, 2, and 3
  and is byte-for-byte compatible with knife/chef-client. The AES key is
  `sha256(secret)`; values round-trip through Chef's `json_wrapper` boxing.
- `Policies.PushRevision(lockJSON, group, cookbooks)` — the server-side half of
  `chef push`: upload each pinned cookbook as an artifact, then associate the
  revision with a policy group. The lock bytes are sent verbatim so no fields
  are lost.
- `LoadChefignore(dir)` / `Chefignore.Ignores(relPath)` — knife's chefignore
  matcher (full path, basename, and ancestor-directory globs), so cookbook
  uploads, archives, and identifier computation agree on which files belong to
  a cookbook. `LocalCookbookFromDir` honors it, so chefignored files are
  excluded from an upload.
- `ACL`/`ACE` merge helpers — `ACL.ACEFor(perm)` selects the ACE for one
  permission, `ACE.AddMembers`/`RemoveMembers` dedupe-add or remove actors and
  groups (reporting whether anything changed), and `ExpandPerm("all")` expands
  to the five standard permissions — the reusable core of an ACL grant/revoke.

## License

See LICENSE.
