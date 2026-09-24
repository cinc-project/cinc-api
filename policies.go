package cinc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// PolicyListEntry is the per-policy value in the /policies index. The
// Revisions map's keys are revision identifiers; values are intentionally
// empty objects in the wire format.
type PolicyListEntry struct {
	URI       string                     `json:"uri"`
	Revisions map[string]json.RawMessage `json:"revisions"`
}

// PolicyRevisions is the body returned by GET /policies/NAME.
type PolicyRevisions struct {
	Revisions map[string]json.RawMessage `json:"revisions"`
}

// PolicyRevision is a single Policyfile document. The shape mirrors the
// well-known top-level fields; unknown fields are preserved through the
// Extra map when round-tripping.
type PolicyRevision struct {
	RevisionID           string                  `json:"revision_id,omitempty"`
	Name                 string                  `json:"name,omitempty"`
	RunList              []string                `json:"run_list,omitempty"`
	NamedRunLists        map[string][]string     `json:"named_run_lists,omitempty"`
	CookbookLocks        map[string]CookbookLock `json:"cookbook_locks,omitempty"`
	DefaultAttributes    map[string]any          `json:"default_attributes,omitempty"`
	OverrideAttributes   map[string]any          `json:"override_attributes,omitempty"`
	SolutionDependencies json.RawMessage         `json:"solution_dependencies,omitempty"`
	IncludedPolicyLocks  []json.RawMessage       `json:"included_policy_locks,omitempty"`
}

// CookbookLock is a single cookbook pinning inside a PolicyRevision.
type CookbookLock struct {
	Version                 string          `json:"version,omitempty"`
	Identifier              string          `json:"identifier,omitempty"`
	DottedDecimalIdentifier string          `json:"dotted_decimal_identifier,omitempty"`
	Source                  string          `json:"source,omitempty"`
	CacheKey                string          `json:"cache_key,omitempty"`
	SCMInfo                 json.RawMessage `json:"scm_info,omitempty"`
	SourceOptions           map[string]any  `json:"source_options,omitempty"`
}

// SourceKind identifies how a Policyfile cookbook lock is sourced. The values
// match the source_options keys Chef writes into a Policyfile.lock.json.
type SourceKind string

// The source kinds a cookbook lock can name, one per source_options key.
const (
	SourcePath           SourceKind = "path"
	SourceArtifactserver SourceKind = "artifactserver"
	SourceGit            SourceKind = "git"
	SourceChefServer     SourceKind = "chef_server"
)

// Origin reports how the locked cookbook is sourced and the associated
// location — a filesystem path, a git URL, an artifactserver URL, or a chef
// server URL — read from source_options. When more than one source key is
// present they are preferred in the order path, artifactserver, git,
// chef_server. It returns an error if no recognized source key is present or
// its value is not a string.
func (l CookbookLock) Origin() (SourceKind, string, error) {
	for _, k := range []SourceKind{SourcePath, SourceArtifactserver, SourceGit, SourceChefServer} {
		v, ok := l.SourceOptions[string(k)]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return "", "", fmt.Errorf("cinc: source_options.%s is not a string", k)
		}
		return k, s, nil
	}
	return "", "", fmt.Errorf("cinc: unsupported or missing cookbook source in source_options")
}

// DottedIdentifier returns the lock's dotted_decimal_identifier (the
// identifier rendered as a cookbook version, e.g. "1234.5678.9012"), falling
// back to its identifier when the lock has none. It names the directory a
// cookbook occupies in a legacy-layout policy export,
// cookbooks/NAME-DOTTED_IDENTIFIER; chef-cli's current layout is
// cookbook_artifacts/NAME-IDENTIFIER.
func (l CookbookLock) DottedIdentifier() string {
	if l.DottedDecimalIdentifier != "" {
		return l.DottedDecimalIdentifier
	}
	return l.Identifier
}

// GitRef returns the git revision to check out for a git-sourced lock: the
// first non-empty string among the source_options "revision" (the resolved
// commit, which chef-cli always records), "ref", "tag" and "branch". It
// returns "" when there is none.
func (l CookbookLock) GitRef() string {
	for _, k := range []string{"revision", "ref", "tag", "branch"} {
		if v := l.sourceOption(k); v != "" {
			return v
		}
	}
	return ""
}

// GitSubdir returns the source_options "rel": the cookbook's directory
// within its git repository, or "" when the cookbook is the repository root.
// It comes from an untrusted lock, so confine it to the checkout before use.
func (l CookbookLock) GitSubdir() string {
	return l.sourceOption("rel")
}

// sourceOption returns source_options[key] when it is a string, else "".
func (l CookbookLock) sourceOption(key string) string {
	v, _ := l.SourceOptions[key].(string)
	return v
}

// PinnedVersion returns the version the lock pins: the source_options
// "version" when present and non-empty, otherwise the lock's top-level
// Version.
func (l CookbookLock) PinnedVersion() string {
	if v := l.sourceOption("version"); v != "" {
		return v
	}
	return l.Version
}

// PoliciesService accesses the /policies endpoints.
type PoliciesService struct{ client *Client }

// List returns every policy and its revision ids.
func (s *PoliciesService) List(ctx context.Context) (map[string]PolicyListEntry, *Response, error) {
	return do[map[string]PolicyListEntry](ctx, s.client, "GET",
		s.client.orgPath("/policies"), nil)
}

// Get returns the set of revisions known for a single policy name.
func (s *PoliciesService) Get(ctx context.Context, name string) (*PolicyRevisions, *Response, error) {
	r, resp, err := do[PolicyRevisions](ctx, s.client, "GET",
		s.client.orgPath("/policies/"+esc(name)), nil)
	return ptrOrNil(r, err), resp, err
}

// Delete removes a policy and every revision under it.
func (s *PoliciesService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/policies/"+esc(name)), nil)
	return resp, err
}

// GetRevision fetches a single revision of a policy.
func (s *PoliciesService) GetRevision(ctx context.Context, name, revisionID string) (*PolicyRevision, *Response, error) {
	r, resp, err := do[PolicyRevision](ctx, s.client, "GET",
		s.client.orgPath("/policies/"+esc(name)+"/revisions/"+esc(revisionID)), nil)
	return ptrOrNil(r, err), resp, err
}

// CreateRevision uploads a new revision of a policy. The body is passed
// through as-is, so callers may supply a *PolicyRevision, a map, or any
// other JSON-marshallable value matching the Policyfile schema.
func (s *PoliciesService) CreateRevision(ctx context.Context, name string, doc any) (*PolicyRevision, *Response, error) {
	r, resp, err := do[PolicyRevision](ctx, s.client, "POST",
		s.client.orgPath("/policies/"+esc(name)+"/revisions"), doc)
	return ptrOrNil(r, err), resp, err
}

// DeleteRevision removes a single revision of a policy.
func (s *PoliciesService) DeleteRevision(ctx context.Context, name, revisionID string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/policies/"+esc(name)+"/revisions/"+esc(revisionID)), nil)
	return resp, err
}

// PushResult reports what PoliciesService.PushRevision did.
type PushResult struct {
	// Revision is the server's answer to the policy group association.
	Revision *PolicyRevision
	// Uploaded names the cookbook locks whose artifacts this push uploaded,
	// sorted. It is never nil.
	Uploaded []string
	// AlreadyPresent names the cookbook locks whose artifacts the server
	// already held under the locked identifier, including one a concurrent
	// push stored first (answered with 409), sorted. It is never nil.
	AlreadyPresent []string
}

// PushRevision deploys a Policyfile lock to a policy group: it uploads each
// cookbook the lock pins as a cookbook artifact (under the lock's name and
// the identifier the lock records), then associates the resulting revision
// with group. This is the server-side half of `chef push`.
//
// lockJSON is the raw Policyfile.lock.json; it is parsed (and validated, see
// ParsePolicyfileLock) to discover the policy name and cookbook locks, and
// sent verbatim to the server so no lock fields are lost. cookbooks maps each
// cookbook-lock name to the on-disk cookbook to upload for it — callers fetch
// these from the lock's sources first. The caller's LocalCookbooks are not
// modified.
//
// Each artifact is uploaded under its cookbook-lock name, since that is the
// name chef-client resolves the lock's run list against. A cookbook whose
// name LocalCookbookFromDir took from its directory (its metadata declares
// none) is renamed to the lock's; one whose name or metadata name is a
// different cookbook is an error, as is one whose version differs from the
// lock's, since the lock would then pin an artifact that is not the cookbook
// it describes. All of this is checked before anything is sent.
//
// Chef Server rejects a PUT of an existing artifact identifier with 409, so,
// like chef-cli, PushRevision lists the server's artifacts once and uploads
// only the identifiers it lacks; a 409 from a concurrent pusher is treated as
// already present, since the identifier is a content hash. The PushResult
// says which were which. The artifact uploads and the group association are
// not atomic, but this makes a failed push safe to retry and lets one lock be
// pushed to several groups.
func (s *PoliciesService) PushRevision(ctx context.Context, lockJSON []byte, group string, cookbooks map[string]*LocalCookbook) (*PushResult, *Response, error) {
	lock, err := ParsePolicyfileLock(lockJSON)
	if err != nil {
		return nil, nil, err
	}
	names := sortedKeys(lock.CookbookLocks)
	artifacts := make(map[string]*LocalCookbook, len(names))
	for _, name := range names {
		a, err := lockArtifact(name, lock.CookbookLocks[name], cookbooks[name])
		if err != nil {
			return nil, nil, err
		}
		artifacts[name] = a
	}
	result := &PushResult{Uploaded: []string{}, AlreadyPresent: []string{}}
	if len(names) > 0 {
		remote, _, err := s.client.CookbookArtifacts.List(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("cinc: list cookbook artifacts: %w", err)
		}
		for _, name := range names {
			id := lock.CookbookLocks[name].Identifier
			if remote[name].Has(id) {
				result.AlreadyPresent = append(result.AlreadyPresent, name)
				continue
			}
			err := s.client.CookbookArtifacts.Upload(ctx, artifacts[name], id)
			switch {
			case errors.Is(err, ErrConflict):
				result.AlreadyPresent = append(result.AlreadyPresent, name)
			case err != nil:
				return nil, nil, fmt.Errorf("cinc: push %s (%s): %w", name, id, err)
			default:
				result.Uploaded = append(result.Uploaded, name)
			}
		}
	}
	rev, resp, err := s.client.PolicyGroups.PutPolicy(ctx, group, lock.Name, json.RawMessage(lockJSON))
	if err != nil {
		return nil, resp, err
	}
	result.Revision = rev
	return result, resp, nil
}

// lockArtifact returns a copy of cb to upload for the cookbook lock name,
// named after the lock, or an error if cb is missing or is not the cookbook
// the lock describes (see PushRevision).
func lockArtifact(name string, cl CookbookLock, cb *LocalCookbook) (*LocalCookbook, error) {
	if cl.Identifier == "" {
		return nil, fmt.Errorf("cinc: cookbook lock %q has no identifier", name)
	}
	if cb == nil {
		return nil, fmt.Errorf("cinc: no cookbook supplied for lock %q", name)
	}
	a := *cb
	if !a.nameFromDir {
		for _, own := range []string{a.Name, a.Metadata.Name} {
			if own != "" && own != name {
				return nil, fmt.Errorf("cinc: the cookbook supplied for lock %q is named %q; a cookbook lock must be pushed with the cookbook it names", name, own)
			}
		}
	}
	a.Name, a.Metadata.Name = name, name
	if cl.Version != "" && a.Version != cl.Version {
		return nil, fmt.Errorf("cinc: the cookbook supplied for lock %q is version %q, but the lock pins %q", name, a.Version, cl.Version)
	}
	return &a, nil
}

// sortedKeys returns the keys of m in lexical order, so cookbook uploads run
// in a deterministic sequence.
func sortedKeys(m map[string]CookbookLock) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
