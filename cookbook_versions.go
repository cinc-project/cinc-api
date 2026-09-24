package cinc

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// LatestVersion is the version the Chef Server resolves to the highest
// version of a cookbook, e.g. Cookbooks.Get(ctx, "nginx", LatestVersion).
const LatestVersion = "_latest"

// CompareCookbookVersions compares two cookbook versions the way Chef does,
// numerically by major, minor and patch ("x.y" is "x.y.0"), and returns -1,
// 0 or +1 as a is older than, equal to or newer than b.
//
// A string that is not a valid cookbook version ("x.y" or "x.y.z") is older
// than every valid one, and two such strings compare as strings, so the
// ordering is total: sorting with it is deterministic whatever a server sends.
func CompareCookbookVersions(a, b string) int {
	av, aerr := parseChefVersion(a)
	bv, berr := parseChefVersion(b)
	switch {
	case aerr != nil && berr != nil:
		return strings.Compare(a, b)
	case aerr != nil:
		return -1
	case berr != nil:
		return 1
	}
	for i := range av {
		if c := cmp.Compare(av[i], bv[i]); c != 0 {
			return c
		}
	}
	return 0
}

// allVersions is the num_versions limit meaning "every version".
const allVersions = -1

// numVersionsLimit validates a num_versions argument the way erchef does
// ("all" or a non-negative integer) and returns it as a limit, allVersions
// for "all". An empty numVersions sends no parameter, and its limit is def,
// the server's default for the endpoint.
func numVersionsLimit(numVersions string, def int) (int, error) {
	switch numVersions {
	case "":
		return def, nil
	case "all":
		return allVersions, nil
	}
	n, err := strconv.Atoi(numVersions)
	if err != nil || n < 0 || numVersions[0] == '+' {
		return 0, fmt.Errorf("cinc: invalid num_versions %q: want \"all\" or a non-negative integer", numVersions)
	}
	return n, nil
}

// withNumVersions appends the num_versions query parameter to path, unless
// numVersions is empty.
func withNumVersions(path, numVersions string) string {
	if numVersions == "" {
		return path
	}
	return path + "?num_versions=" + url.QueryEscape(numVersions)
}

// normalizeVersions sorts e's versions newest-first and trims them to limit.
// Servers differ in the order they send and in which endpoints honor
// num_versions, so the client does both itself.
func (e *CookbookListEntry) normalizeVersions(limit int) {
	slices.SortStableFunc(e.Versions, func(a, b CookbookVersion) int {
		return CompareCookbookVersions(b.Version, a.Version)
	})
	if limit != allVersions && len(e.Versions) > limit {
		e.Versions = e.Versions[:limit]
	}
}

// getCookbookList GETs a name -> {url, versions} listing with the given
// num_versions, whose empty value means the endpoint's default limit def,
// and returns it with every entry's versions newest-first and within the
// limit. An invalid numVersions fails before any request is sent.
func getCookbookList(ctx context.Context, c *Client, path, numVersions string, def int) (map[string]CookbookListEntry, *Response, error) {
	limit, err := numVersionsLimit(numVersions, def)
	if err != nil {
		return nil, nil, err
	}
	m, resp, err := do[map[string]CookbookListEntry](ctx, c, "GET", withNumVersions(path, numVersions), nil)
	if err != nil {
		return nil, resp, err
	}
	for name, e := range m {
		e.normalizeVersions(limit)
		m[name] = e
	}
	return m, resp, nil
}
