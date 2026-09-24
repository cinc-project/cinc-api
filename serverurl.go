package cinc

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseServerURL splits a Chef/CINC Server URL of the form
// https://host[:port]/organizations/<org> into its base server URL
// (scheme://host[:port]) and organization name.
//
// It is the parsing counterpart to NewClient, which takes the base ServerURL
// and Org separately: callers that hold a single combined URL (as Chef's
// credentials files and CHEF_SERVER_URL store it) use this to derive the two.
func ParseServerURL(raw string) (serverURL, org string, err error) {
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("cinc: invalid server URL %q", raw)
	}
	// Split the escaped path, so an org escaped by FormatServerURL (a "/" as
	// %2F, say) stays one segment, then unescape the org.
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 2 || parts[0] != "organizations" || parts[1] == "" {
		return "", "", fmt.Errorf("cinc: server URL %q must end with /organizations/<org>", raw)
	}
	// url.Parse has already rejected a bad escape, and EscapedPath only
	// produces valid ones, so this cannot fail.
	org, _ = url.PathUnescape(parts[1])
	return u.Scheme + "://" + u.Host, org, nil
}

// FormatServerURL joins a base server URL and an organization into the
// combined https://host[:port]/organizations/<org> form that Chef's
// credentials files and CHEF_SERVER_URL use. It is the inverse of
// ParseServerURL: trailing slashes on serverURL are trimmed and org is
// path-escaped.
func FormatServerURL(serverURL, org string) string {
	return strings.TrimRight(serverURL, "/") + "/organizations/" + url.PathEscape(org)
}
