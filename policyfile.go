package cinc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// Chef Server validates a policy revision against chef_regex (erchef's
// oc_chef_policy_revision VALIDATION_CONSTRAINTS); ParsePolicyfileLock applies
// the same patterns so a lock the server would refuse fails before anything
// is uploaded, and so the names it returns are safe to use as path segments
// and in generated config. [:alnum:] there is ASCII.
var (
	// policyNameRE is chef_regex's policy_file_name, which is also its
	// policy_file_revision_id and policy_identifier (NAME_REGEX_MAX_255).
	policyNameRE = regexp.MustCompile(`^[.A-Za-z0-9_:-]{1,255}$`)
	// cookbookLockNameRE is chef_regex's cookbook_name (NAME_REGEX), which
	// erchef applies to each cookbook_locks key.
	cookbookLockNameRE = regexp.MustCompile(`^[.A-Za-z0-9_-]+$`)
	// policyRecipeRE is chef_regex's policy_fully_qualified_recipe: a policy
	// run list names only recipe[cookbook::recipe], with no roles, bare
	// cookbooks or version pins.
	policyRecipeRE = regexp.MustCompile(`^recipe\[[.A-Za-z0-9_-]+::[.A-Za-z0-9_-]+\]$`)
)

// validateLockForPush applies the rest of the rules erchef checks a policy
// revision against (oc_chef_policy_revision VALIDATION_CONSTRAINTS) when it is
// PUT to a policy group, on top of validatePolicyfileLock's: a revision id, a
// run_list of fully qualified recipes, cookbook_locks, and a valid version
// (and dotted_decimal_identifier, when given) on every lock. ParsePolicyfileLock
// leaves these to the caller; PushRevision cannot, because the server only
// checks them after every artifact has been uploaded.
func validateLockForPush(lock *PolicyRevision) error {
	if lock.RevisionID == "" {
		return errors.New("cinc: policyfile lock has no revision_id")
	}
	if lock.RunList == nil {
		return errors.New("cinc: policyfile lock has no run_list")
	}
	for _, item := range lock.RunList {
		if !policyRecipeRE.MatchString(item) {
			return fmt.Errorf("cinc: policyfile lock run_list item %q is not a fully qualified recipe[cookbook::recipe]", item)
		}
	}
	if lock.CookbookLocks == nil {
		return errors.New("cinc: policyfile lock has no cookbook_locks")
	}
	for _, name := range sortedKeys(lock.CookbookLocks) {
		cl := lock.CookbookLocks[name]
		if cl.Version == "" {
			return fmt.Errorf("cinc: cookbook lock %q has no version", name)
		}
		if !validLockVersion(cl.Version) {
			return fmt.Errorf("cinc: cookbook lock %q has an invalid version %q", name, cl.Version)
		}
		if cl.DottedDecimalIdentifier != "" && !validLockVersion(cl.DottedDecimalIdentifier) {
			return fmt.Errorf("cinc: cookbook lock %q has an invalid dotted_decimal_identifier %q", name, cl.DottedDecimalIdentifier)
		}
	}
	return nil
}

// validLockVersion mirrors chef_cookbook_version:valid_cookbook_constraint,
// which erchef applies to a lock's version: an x.y[.z] version, optionally
// after a constraint operator.
func validLockVersion(v string) bool {
	if m := chefConstraintRe.FindStringSubmatch(v); m != nil {
		v = m[2]
	}
	_, err := parseChefVersion(v)
	return err == nil
}

// ParsePolicyfileLock unmarshals the bytes of a Policyfile.lock.json into a
// PolicyRevision, which models the lock's structure (name, run lists, cookbook
// locks, attributes, solution dependencies). Use it to inspect a lock — for
// example to discover which cookbooks must be fetched before a push.
//
// It rejects a lock the server would refuse for its names: the policy name
// and a non-empty revision_id must be 1 to 255 of A-Z, a-z, 0-9, _, -, : and
// .; each cookbook lock name must be A-Z, a-z, 0-9, _, - and . only; and a
// non-empty cookbook lock identifier follows the policy name rule. A missing
// revision_id or identifier is left for the server (or PushRevision) to
// refuse, so a partial lock can still be inspected.
//
// To deploy a lock, prefer Policies.PushRevision with the original bytes: a
// lock can carry fields PolicyRevision does not model (such as a cookbook
// lock's "origin"), so re-marshalling a parsed value would drop them.
func ParsePolicyfileLock(data []byte) (*PolicyRevision, error) {
	var lock PolicyRevision
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("cinc: parse policyfile lock: %w", err)
	}
	if lock.Name == "" {
		return nil, fmt.Errorf("cinc: policyfile lock is missing a policy name")
	}
	if err := validatePolicyfileLock(&lock); err != nil {
		return nil, err
	}
	return &lock, nil
}

// validatePolicyfileLock checks the lock's names against the server's rules
// (see policyNameRE).
func validatePolicyfileLock(lock *PolicyRevision) error {
	const policyRule = "the server accepts 1 to 255 letters (A-Z, a-z), digits, dots, underscores, hyphens and colons"
	if !policyNameRE.MatchString(lock.Name) {
		return fmt.Errorf("cinc: policyfile lock has an invalid policy name %q: %s", lock.Name, policyRule)
	}
	if lock.RevisionID != "" && !policyNameRE.MatchString(lock.RevisionID) {
		return fmt.Errorf("cinc: policyfile lock has an invalid revision id %q: %s", lock.RevisionID, policyRule)
	}
	for _, name := range sortedKeys(lock.CookbookLocks) {
		if !cookbookLockNameRE.MatchString(name) {
			return fmt.Errorf("cinc: policyfile lock has an invalid cookbook lock name %q: the server accepts only letters (A-Z, a-z), digits, dots, underscores and hyphens", name)
		}
		if id := lock.CookbookLocks[name].Identifier; id != "" && !policyNameRE.MatchString(id) {
			return fmt.Errorf("cinc: cookbook lock %q has an invalid identifier %q: %s", name, id, policyRule)
		}
	}
	return nil
}

// LoadPolicyfileLock reads and parses a Policyfile.lock.json from disk. It
// returns the parsed lock alongside the original bytes, which callers pass
// verbatim to Policies.PushRevision so no lock fields are lost on the round
// trip.
func LoadPolicyfileLock(path string) (*PolicyRevision, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: read policyfile lock: %w", err)
	}
	lock, err := ParsePolicyfileLock(data)
	if err != nil {
		return nil, nil, err
	}
	return lock, data, nil
}
