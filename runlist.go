package cinc

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ErrInvalidRunListItem means a run-list entry is not one a Chef Server
// accepts; ValidateRunListItem wraps it.
var ErrInvalidRunListItem = errors.New("cinc: invalid run list item")

// The patterns erchef's chef_json_validator:run_list_spec checks each entry
// against (chef_regex's qualified_role, qualified_recipe and
// unqualified_recipe): names are NAME_REGEX ([.[:alnum:]_-]+, ASCII), a recipe
// may be cookbook-qualified ("cookbook::recipe") and may carry a version
// ("@1.2" or "@1.2.3"), and a role has neither.
var (
	runListRoleRE   = regexp.MustCompile(`^role\[[.A-Za-z0-9_-]+\]$`)
	runListRecipeRE = regexp.MustCompile(`^(?:[.A-Za-z0-9_-]+::)?[.A-Za-z0-9_-]+(?:@[0-9]+(?:\.[0-9]+){1,2})?$`)
)

// ValidateRunListItem reports whether item is a run-list entry a Chef Server
// accepts, so a caller can refuse a malformed one (such as "recipe[") before
// the server answers the whole save with a 400. As erchef does, it picks the
// pattern by prefix: "role[NAME]"; "recipe[...]" around a recipe; and
// anything else is a bare recipe, "COOKBOOK" or "COOKBOOK::RECIPE", either
// optionally followed by "@VERSION" with two or three numeric parts. Names use
// only ASCII letters, digits, '_', '-' and '.'. A failure wraps
// ErrInvalidRunListItem and names the item.
//
// This mirrors erchef's chef_json_validator:valid_run_list_item/1
// (chef-server/src/oc_erchef/apps/chef_objects/src/chef_json_validator.erl,
// with the regexes in chef_regex.erl) and cinc-server-ng's validRunList.
// NormalizeRunList does not call it; validate entries first when they come
// from a user.
func ValidateRunListItem(item string) error {
	switch {
	case strings.HasPrefix(item, "role["):
		if !runListRoleRE.MatchString(item) {
			return fmt.Errorf("%w %q: a role must be role[NAME], and NAME can use only letters, digits, '.', '_' and '-'", ErrInvalidRunListItem, item)
		}
		return nil
	case strings.HasPrefix(item, "recipe["):
		if inner, ok := strings.CutSuffix(item[len("recipe["):], "]"); ok && runListRecipeRE.MatchString(inner) {
			return nil
		}
	case runListRecipeRE.MatchString(item):
		return nil
	}
	return fmt.Errorf("%w %q: a recipe must be COOKBOOK or COOKBOOK::RECIPE, optionally in recipe[...] and followed by @VERSION (such as @1.2.3), and names can use only letters, digits, '.', '_' and '-'", ErrInvalidRunListItem, item)
}

// NormalizeRunListItem returns a run-list entry in the explicit form a Chef
// Server stores: "role[...]" and "recipe[...]" are returned unchanged, and
// anything else is taken to be a recipe and wrapped, so "nginx" becomes
// "recipe[nginx]" and "nginx::server" becomes "recipe[nginx::server]".
//
// This mirrors erchef's chef_object_base:normalize_item/1
// (chef-server/src/oc_erchef/apps/chef_objects/src/chef_object_base.erl),
// which, like this function, assumes the item has already been validated.
func NormalizeRunListItem(item string) string {
	if strings.HasPrefix(item, "role[") || strings.HasPrefix(item, "recipe[") {
		return item
	}
	return "recipe[" + item + "]"
}

// NormalizeRunList returns the run list a Chef Server would store for items:
// each entry normalized with NormalizeRunListItem, then exact duplicates
// dropped, keeping the first occurrence and the original order. Semantic
// duplicates such as "recipe[nginx]" and "recipe[nginx::default]" are kept.
// The result is never nil, so it encodes as a JSON array, and items is not
// modified.
//
// This mirrors erchef's chef_object_base:normalize_run_list/1, which erchef
// applies to a node's run_list (chef_node.erl) and to a role's run_list and
// each of its env_run_lists (chef_role.erl) on every save.
func NormalizeRunList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if n := NormalizeRunListItem(item); !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// addRunListItems returns base with items appended, normalized as a Chef
// Server would store the result, so an item already present under either
// spelling ("nginx" or "recipe[nginx]") is not added twice.
func addRunListItems(base, items []string) []string {
	return NormalizeRunList(append(slices.Clone(base), items...))
}

// removeRunListItems returns base, normalized, without any entry whose
// normalized form matches a normalized item, so "nginx" removes a stored
// "recipe[nginx]". Only exact matches after normalization are removed:
// "nginx" does not remove "recipe[nginx::default]".
func removeRunListItems(base, items []string) []string {
	drop := NormalizeRunList(items)
	return slices.DeleteFunc(NormalizeRunList(base), func(entry string) bool {
		return slices.Contains(drop, entry)
	})
}

// AddRunListItems appends entries to the role's run list, normalized the way
// the Chef Server stores it (see NormalizeRunList), so an entry already
// present under either spelling is not duplicated. EnvRunLists is untouched.
func (r *Role) AddRunListItems(items ...string) {
	r.RunList = addRunListItems(r.RunList, items)
}

// RemoveRunListItems removes entries from the role's run list, comparing
// normalized forms, so "nginx" removes "recipe[nginx]". The remaining list is
// normalized as well. EnvRunLists is untouched.
func (r *Role) RemoveRunListItems(items ...string) {
	r.RunList = removeRunListItems(r.RunList, items)
}
