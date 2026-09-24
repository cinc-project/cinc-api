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
| `c.ACLs`             | `/<object>/<name>/_acl`               | GetTarget / SetTargetPermission / Grant / Revoke on an `ACLTarget` (`ObjectACL`, `OrgACL`, `UserACL`); Get / SetPermission, GetOrg / SetOrgPermission and GetUser / SetUserPermission |
| `c.Associations`     | `/organizations/O/users`, `/association_requests`, `/users/U/...` | Members (ListMembers/GetMember/AddMember/RemoveMember), org invites (ListInvites/Invite/RescindInvite), user invites (ListUserInvites/UserInviteCount/RespondInvite) and ListUserOrgs |
| `c.Clients`          | `/clients`                            | List / Get / Create / Update / Delete / Reregister                   |
| `c.Containers`       | `/containers`                         | List / Get / Create / Delete                                         |
| `c.Cookbooks`        | `/cookbooks`                          | List (latest version of each) / ListVersions (every cookbook, `num_versions`) / GetVersions (one cookbook, `num_versions`) / Get (with metadata) / Delete / Upload (sandbox flow) / Download / DownloadFiles (from a fetched manifest) / ListLatest / ListRecipes |
| `c.CookbookArtifacts`| `/cookbook_artifacts`                 | List / GetVersions (one artifact) / Get (with metadata) / Delete / Upload |
| `c.DataBags`         | `/data`                               | List / Create / Delete; per-bag Items handle for CRUD plus GetDecrypted / CreateEncrypted / UpdateEncrypted; `DataBagItem.Encrypt`/`Decrypt`/`IsEncrypted` for the Chef encrypted-data-bag format (writes v3 AES-256-GCM, reads v1/v2/v3) |
| `c.Environments`     | `/environments`                       | List / Get / Create / Update / Delete / ListCookbooks / GetCookbook / CookbookVersions / ListNodes / ListRecipes / RoleRunList |
| `c.Groups`           | `/groups`                             | List / Get / Create / Update / Delete / AddMembers / RemoveMembers   |
| `c.Keys`             | `/users/U/keys`, `/clients/C/keys`    | `User(name)` / `Client(name)` → List / Get / Create / Update / Delete |
| `c.License`          | `/license`                            | Get (node-license usage)                                             |
| `c.Nodes`            | `/nodes`                              | List / Get / Create / Update / Modify / Delete                       |
| `c.Orgs`             | `/organizations` (top-level)          | List / Get / Create / Update / Delete                                |
| `c.Policies`         | `/policies`                           | List / Get / Delete / GetRevision / CreateRevision / DeleteRevision / PushRevision |
| `c.PolicyGroups`     | `/policy_groups`                      | List / Get / Delete / GetPolicy / PutPolicy / DeletePolicy           |
| `c.Principals`       | `/principals/<name>`                  | Get (public key(s) + type for a user/client)                        |
| `c.PushJobs`         | `/pushy/jobs`                         | Create (start a job) / Get (job status + per-node outcomes)         |
| `c.RequiredRecipe`   | `/required_recipe`                    | Get (returns Ruby text/plain)                                        |
| `c.Roles`            | `/roles`                              | List / Get / Create / Update / Delete / Environments / EnvironmentRunList |
| `c.Search`           | `/search/INDEX`                       | `Query` (with `WithStart`/`WithRows`/`WithPartial`/`WithPartialPaths`), `All` (`iter.Seq2` over rows, one page in memory at a time), `Nodes` (rows decoded as `*Node`), `DataBagItems` (rows unwrapped to `DataBagItem`), `SearchAll`, `Indexes` |
| `c.Stats`            | `/_stats` (top-level, Basic auth)     | Get (Erchef/PostgreSQL/VM metrics; not Chef-signed)                 |
| `c.Status`           | `/_status`                            | Get (server health + keygen pool)                                    |
| `c.Universe`         | `/universe` (org + top-level)         | Get / GetGlobal (known cookbooks + dependencies)                    |
| `c.Users`            | `/users` (top-level)                  | List / Get / Create / Update / Delete / Authenticate                 |

Configurable via options: `WithHTTPClient`, `WithUserAgent`,
`WithChefVersion`, `WithSkipTLSVerify`, `WithMaxRetries`,
`WithTransferTimeout`. Idempotent GETs are retried on 5xx and network errors,
but not on a failed TLS certificate check. Any other request is retried only
on `503 Service Unavailable`, which means the server did not process it (a
Chef Server under load answers `POST /users` and `POST /clients` this way);
a `Retry-After` header on the 503 is honoured, up to 10 seconds. Signed
requests never follow redirects, since that would hand the signature to the
redirect target: a 3xx comes back as an `*ErrorResponse` naming the
`Location`. `NewClient` applies this to a copy of the `http.Client`, so one
passed with `WithHTTPClient` is left as it was. Cookbook file transfers to and from the pre-signed bookshelf URLs (the sandbox
PUTs of an upload, the GETs of a download) are retried the same way; the PUTs
are safe to repeat because they are addressed by content checksum. Those
transfers are bounded by `WithTransferTimeout` (default 10 minutes per
attempt, `0` for no limit beyond the context) instead of the `http.Client`
timeout, so a large file on a slow link is not cut off by the 30-second API
timeout.

### Helpers

Standalone helpers for working with Chef/CINC identities and the node object
model, so callers don't re-encode server conventions:

- `ParseServerURL(raw)` — split `https://host/organizations/<org>` into the
  base server URL and org (the inverse of `NewClient`'s `ServerURL`/`Org`).
- `GenerateKeyPair()` — mint a 2048-bit RSA key pair as PEM (the generation
  counterpart to `ParseKey`/`LoadKeyFile`).
- `Node` accessors — `Tags`/`SetTags`/`AddTags`/`RemoveTags` (stored at
  `normal.tags`), `AddRunListItems`/`RemoveRunListItems`,
  `Attribute`/`AttributeString` (precedence-aware lookup, dotted paths),
  `LastCheckin()` (from `automatic.ohai_time`), and `EnvironmentName()`
  (`_default` when unset).
- `NormalizeRunListItem(item)` / `NormalizeRunList(items)` — the run-list
  form erchef stores: a bare `nginx` becomes `recipe[nginx]`, then exact
  duplicates are dropped in order. `Node` and `Role`
  `AddRunListItems`/`RemoveRunListItems` compare and write normalized
  entries, so `nginx` and `recipe[nginx]` are the same item.
- `Nodes.Modify(name, fn)` — read-modify-write: get the node, apply `fn`,
  and PUT it only if its encoding changed (a rename is refused). Nodes
  have no optimistic concurrency, so a concurrent write in between (such as
  a chef-client run) is overwritten.
- `Clients.Create` asks the server to generate the client's `default` keypair
  (returned in `ChefKey.PrivateKey`) unless `APIClient.PublicKey` is set.
- `Clients.Reregister(name)` — regenerate a client's `default` key and return
  the new private key (creating one if the client has none).
- `ParsePolicyfileLock(data)` / `LoadPolicyfileLock(path)` — parse a
  `Policyfile.lock.json` into a `PolicyRevision`.
- `CompareCookbookVersions(a, b)` orders cookbook versions the way Chef does
  (numeric `x.y[.z]`, so `10.0.0` is newer than `9.0.0`); anything that is not
  a valid version sorts below every valid one. Every version list the client
  returns (`Cookbooks.List`/`ListVersions`/`GetVersions`,
  `Environments.ListCookbooks`/`GetCookbook`) is sorted newest-first with it
  and trimmed to `num_versions`, whatever the server sent, and an invalid
  `num_versions` is rejected before a request is made. `LatestVersion` is the
  `_latest` version alias.
- `CookbookLock` accessors — `Origin()` (classify a lock's `source_options` as
  `path`/`artifactserver`/`git`/`chef_server` and return its location) and
  `PinnedVersion()` (the `source_options` version, falling back to the lock's
  top-level version).
- `DataBagItem.Encrypt(secret)` / `Decrypt(secret)` / `IsEncrypted()` — the
  Chef encrypted-data-bag-item codec. `Encrypt` boxes every value except `id`
  in a version-3 (AES-256-GCM) wrapper; `Decrypt` reads versions 1, 2, and 3
  and is byte-for-byte compatible with knife/chef-client. The AES key is
  `sha256(secret)`; values round-trip through Chef's `json_wrapper` boxing.
  `Encrypt` refuses an item that already holds an encrypted value
  (`ErrAlreadyEncrypted`) rather than encrypting the ciphertext again.
- `Items(bag).GetDecrypted(id, secret)` / `CreateEncrypted(item, secret)` /
  `UpdateEncrypted(item, secret)` — the encrypted read and write paths in one
  call each; an edit is `GetDecrypted`, a change, then `UpdateEncrypted`.
- `LoadDataBagSecret(path)` / `ParseDataBagSecret(data)` — read a shared
  secret file exactly as Chef's `EncryptedDataBagItem.load_secret` does:
  leading and trailing NUL and ASCII whitespace stripped, UTF-8 required, an
  empty secret refused (`ErrEmptyDataBagSecret`). Chef's remote (URL) secrets
  are not supported.
- `DataBagItem.Validate()` — the non-empty string `id` check `Create`,
  `Update` and `Encrypt` apply (`ErrMissingDataBagItemID`), for vetting an
  edited item up front. `DataBagItem.Content()` — the item without `id` and
  the `chef_type`/`data_bag` keys a server adds to echoes and search rows.
- `Policies.PushRevision(lockJSON, group, cookbooks)` — the server-side half of
  `chef push`: upload each pinned cookbook as an artifact, then associate the
  revision with a policy group. The lock bytes are sent verbatim so no fields
  are lost. Artifacts the server already has are skipped, so the same lock can
  be pushed to several groups and a failed push can be retried.
- `LoadChefignore(dir)` / `Chefignore.Ignores(relPath)` — Chef's chefignore
  handling: the nearest `chefignore` in `dir` or any parent (so a chef-repo's
  `cookbooks/chefignore` applies), with each pattern matched against the
  cookbook-relative path exactly as Ruby's `File.fnmatch?` does. Cookbook
  uploads, archives, and identifier computation agree on which files belong to
  a cookbook. `LocalCookbookFromDir` honors it, and like Chef's loader also
  skips dot-directories at the cookbook root and takes the cookbook name from
  `metadata.json` / `metadata.rb` rather than the directory name.
- `LocalCookbookFromDir(dir, version)` — load a cookbook for
  `Cookbooks.Upload` / `CookbookArtifacts.Upload`. `LocalCookbook.Metadata`
  (a `CookbookMetadata`: name, version, description, maintainer, license,
  dependencies, platforms, chef/ohai versions, …) is sent as the manifest's
  `metadata` block, which Chef Server requires and chef-client reads. It is
  filled from `metadata.json` when present, otherwise from the literal calls
  in `metadata.rb` (`name`, `version`, `description`, `long_description`,
  `maintainer`, `maintainer_email`, `license`, `source_url`, `issues_url`,
  `depends`, `supports`, `chef_version`, `ohai_version`, `privacy`, each on
  one line with string literal arguments); metadata.rb is Ruby and is never
  evaluated, so anything computed there needs a `metadata.json` or an edit to
  `Metadata`. An empty `version` uses the metadata's version; one that
  disagrees with it is an error.
- `UnwrapSearchRow(row)` — the object a search row describes: the `data` of
  a partial-search row (`{"url", "data"}`), the `raw_data` of the
  `Chef::DataBagItem` envelope a full data bag search wraps each item in, and
  any other row unchanged. `WithPartialPaths("kernel.release", ...)` builds a
  partial search from dotted paths, keyed by the path itself.
- `Groups.AddMembers(group, kind, names...)` / `RemoveMembers(...)` — change
  one kind of member (`MemberUser`, `MemberClient`, `MemberGroup`;
  `ParseMemberKind` reads `"user"`/`"users"` and so on) with a read, a PUT
  that is skipped when nothing changes, and a read-back. The returned
  `MemberChange` splits the names into `Changed`, `Unchanged` (already as
  asked) and `Dropped`: the server accepts a group PUT naming an actor that
  does not exist and silently leaves it out, so a successful PUT alone does
  not mean the member was added.
- `Group` decodes both of the server's shapes: members in top-level
  `users`/`clients`/`groups` arrays (GET) or nested under an `actors` object
  (the PUT body, which erchef echoes back). The flat `actors` array a GET also
  carries is ignored, since the typed lists already hold its names.
- `ACL`/`ACE` merge helpers — `ACL.ACEFor(perm)` selects the ACE for one
  permission, `ACE.AddMembers`/`RemoveMembers` dedupe-add or remove actors and
  groups (reporting whether anything changed), and `ExpandPerm("all")` expands
  to the five standard permissions — the reusable core of an ACL grant/revoke.
- ACL targets and grant/revoke — `ObjectACL(cinc.ACLNodes, "web01")`,
  `OrgACL()` and `UserACL("alice")` name the object whose ACL is read or
  written; the `ACL*` constants (`ACLDataBags` is `"data"`, and so on, listed
  in `ACLObjectTypes`) are the object types erchef serves `_acl` on.
  `ACLs.Grant`/`Revoke(ctx, target, perm, actors, groups)` take a permission
  or `"all"`, read the ACL once, write only the permissions that change, and
  return those; a write that fails part way returns the permissions already
  changed and an `*ACLChangeError` naming the one that failed. A member the
  server cannot resolve is a 400, `ErrBadRequest`.

## License

Licensed under the [Apache License 2.0](LICENSE).
