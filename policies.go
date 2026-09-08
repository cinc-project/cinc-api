package cinc

import (
	"context"
	"encoding/json"
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

// PinnedVersion returns the version the lock pins: the source_options
// "version" when present and non-empty, otherwise the lock's top-level
// Version.
func (l CookbookLock) PinnedVersion() string {
	if v, ok := l.SourceOptions["version"].(string); ok && v != "" {
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
		s.client.orgPath("/policies/"+name), nil)
	return ptrOrNil(r, err), resp, err
}

// Delete removes a policy and every revision under it.
func (s *PoliciesService) Delete(ctx context.Context, name string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/policies/"+name), nil)
	return resp, err
}

// GetRevision fetches a single revision of a policy.
func (s *PoliciesService) GetRevision(ctx context.Context, name, revisionID string) (*PolicyRevision, *Response, error) {
	r, resp, err := do[PolicyRevision](ctx, s.client, "GET",
		s.client.orgPath("/policies/"+name+"/revisions/"+revisionID), nil)
	return ptrOrNil(r, err), resp, err
}

// CreateRevision uploads a new revision of a policy. The body is passed
// through as-is, so callers may supply a *PolicyRevision, a map, or any
// other JSON-marshallable value matching the Policyfile schema.
func (s *PoliciesService) CreateRevision(ctx context.Context, name string, doc any) (*PolicyRevision, *Response, error) {
	r, resp, err := do[PolicyRevision](ctx, s.client, "POST",
		s.client.orgPath("/policies/"+name+"/revisions"), doc)
	return ptrOrNil(r, err), resp, err
}

// DeleteRevision removes a single revision of a policy.
func (s *PoliciesService) DeleteRevision(ctx context.Context, name, revisionID string) (*Response, error) {
	_, resp, err := do[map[string]any](ctx, s.client, "DELETE",
		s.client.orgPath("/policies/"+name+"/revisions/"+revisionID), nil)
	return resp, err
}

// PushRevision deploys a Policyfile lock to a policy group: it uploads every
// cookbook the lock pins as a cookbook artifact (under the identifier the lock
// records), then associates the resulting revision with group. This is the
// server-side half of `chef push`.
//
// lockJSON is the raw Policyfile.lock.json; it is parsed to discover the
// policy name and cookbook locks, and sent verbatim to the server so no lock
// fields are lost. cookbooks maps each cookbook-lock name to the on-disk
// cookbook to upload for it — callers fetch these from the lock's sources
// first (see ParsePolicyfileLock). The two server steps are not atomic, but
// artifact uploads are idempotent by identifier, so a failed push is safe to
// retry.
func (s *PoliciesService) PushRevision(ctx context.Context, lockJSON []byte, group string, cookbooks map[string]*LocalCookbook) (*PolicyRevision, *Response, error) {
	lock, err := ParsePolicyfileLock(lockJSON)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range sortedKeys(lock.CookbookLocks) {
		cl := lock.CookbookLocks[name]
		if cl.Identifier == "" {
			return nil, nil, fmt.Errorf("cinc: cookbook lock %q has no identifier", name)
		}
		cb := cookbooks[name]
		if cb == nil {
			return nil, nil, fmt.Errorf("cinc: no cookbook supplied for lock %q", name)
		}
		if err := s.client.CookbookArtifacts.Upload(ctx, cb, cl.Identifier); err != nil {
			return nil, nil, fmt.Errorf("cinc: push %s (%s): %w", name, cl.Identifier, err)
		}
	}
	return s.client.PolicyGroups.PutPolicy(ctx, group, lock.Name, json.RawMessage(lockJSON))
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
