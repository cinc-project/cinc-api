package cinc

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeRunListItem(t *testing.T) {
	cases := []struct{ in, want string }{
		{"nginx", "recipe[nginx]"},
		{"nginx::default", "recipe[nginx::default]"},
		{"nginx@1.2.3", "recipe[nginx@1.2.3]"},
		{"recipe[nginx]", "recipe[nginx]"},
		{"recipe[nginx::server]", "recipe[nginx::server]"},
		{"role[web]", "role[web]"},
	}
	for _, tc := range cases {
		if got := NormalizeRunListItem(tc.in); got != tc.want {
			t.Errorf("NormalizeRunListItem(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeRunList(t *testing.T) {
	t.Run("qualifies bare recipes and drops exact duplicates in order", func(t *testing.T) {
		got := NormalizeRunList([]string{"role[web]", "nginx", "recipe[base]", "recipe[nginx]", "base", "role[web]"})
		want := []string{"role[web]", "recipe[nginx]", "recipe[base]"}
		if !slices.Equal(got, want) {
			t.Errorf("NormalizeRunList = %v, want %v", got, want)
		}
	})
	t.Run("keeps semantic duplicates, as erchef does", func(t *testing.T) {
		got := NormalizeRunList([]string{"nginx", "nginx::default"})
		want := []string{"recipe[nginx]", "recipe[nginx::default]"}
		if !slices.Equal(got, want) {
			t.Errorf("NormalizeRunList = %v, want %v", got, want)
		}
	})
	t.Run("nil becomes an empty, non-nil list", func(t *testing.T) {
		got := NormalizeRunList(nil)
		if got == nil || len(got) != 0 {
			t.Errorf("NormalizeRunList(nil) = %#v, want []string{}", got)
		}
	})
	t.Run("does not modify its input", func(t *testing.T) {
		in := []string{"nginx", "nginx"}
		NormalizeRunList(in)
		if !slices.Equal(in, []string{"nginx", "nginx"}) {
			t.Errorf("input mutated to %v", in)
		}
	})
}

func TestRunListMutationNormalizes(t *testing.T) {
	t.Run("node add matches a stored qualified recipe", func(t *testing.T) {
		n := &Node{RunList: []string{"recipe[nginx]"}}
		n.AddRunListItems("nginx", "base")
		want := []string{"recipe[nginx]", "recipe[base]"}
		if !slices.Equal(n.RunList, want) {
			t.Errorf("RunList = %v, want %v", n.RunList, want)
		}
	})
	t.Run("node add normalizes the existing list too", func(t *testing.T) {
		n := &Node{RunList: []string{"nginx", "recipe[nginx]"}}
		n.AddRunListItems("role[web]")
		want := []string{"recipe[nginx]", "role[web]"}
		if !slices.Equal(n.RunList, want) {
			t.Errorf("RunList = %v, want %v", n.RunList, want)
		}
	})
	t.Run("node remove matches either spelling", func(t *testing.T) {
		n := &Node{RunList: []string{"recipe[nginx]", "base", "role[web]"}}
		n.RemoveRunListItems("nginx", "recipe[base]")
		want := []string{"role[web]"}
		if !slices.Equal(n.RunList, want) {
			t.Errorf("RunList = %v, want %v", n.RunList, want)
		}
	})
	t.Run("node remove leaves semantic duplicates alone", func(t *testing.T) {
		n := &Node{RunList: []string{"recipe[nginx::default]"}}
		n.RemoveRunListItems("nginx")
		want := []string{"recipe[nginx::default]"}
		if !slices.Equal(n.RunList, want) {
			t.Errorf("RunList = %v, want %v", n.RunList, want)
		}
	})
	t.Run("node remove on empty node yields an empty list", func(t *testing.T) {
		n := &Node{}
		n.RemoveRunListItems("nginx")
		if n.RunList == nil || len(n.RunList) != 0 {
			t.Errorf("RunList = %#v, want []string{}", n.RunList)
		}
	})
	t.Run("role add and remove", func(t *testing.T) {
		r := &Role{RunList: []string{"base"}}
		r.AddRunListItems("recipe[base]", "nginx")
		if want := []string{"recipe[base]", "recipe[nginx]"}; !slices.Equal(r.RunList, want) {
			t.Fatalf("after add, RunList = %v, want %v", r.RunList, want)
		}
		r.RemoveRunListItems("base")
		if want := []string{"recipe[nginx]"}; !slices.Equal(r.RunList, want) {
			t.Errorf("after remove, RunList = %v, want %v", r.RunList, want)
		}
	})
}

// The cases follow erchef's chef_json_validator:run_list_spec, which checks
// each entry against chef_regex's qualified_recipe, qualified_role or
// unqualified_recipe pattern according to its prefix.
func TestValidateRunListItem(t *testing.T) {
	valid := []string{
		"nginx", "nginx::server", "my-cb.v2_x", "nginx@1.2", "nginx@1.2.3", "nginx::server@10.0.1",
		"recipe[nginx]", "recipe[nginx::server]", "recipe[nginx@1.2.3]", "recipe[nginx::server@1.2]",
		"role[web]", "role[web.v2-x_y]",
		// Bare names that merely start like a keyword are recipes.
		"role", "recipe", "roles", "recipes::x",
	}
	for _, item := range valid {
		if err := ValidateRunListItem(item); err != nil {
			t.Errorf("ValidateRunListItem(%q) = %v, want nil", item, err)
		}
	}
	invalid := []string{
		"", "recipe[", "recipe[]", "recipe[nginx", "recipe[nginx]]", "recipe[ nginx]",
		"recipe[nginx::]", "recipe[::server]", "recipe[a::b::c]", "recipe[nginx@1]",
		"recipe[nginx@1.2.3.4]", "recipe[nginx@v1.2]", "recipe[role[web]]",
		"role[", "role[]", "role[web", "role[web::x]", "role[web@1.2]", "role[a:b]", "role[web]x",
		"nginx::", "::nginx", "nginx@", "nginx@1", "ngi nx", "nginx\n", "café", "nginx]", "[nginx]",
		"Role[web]", "recipe [nginx]",
	}
	for _, item := range invalid {
		err := ValidateRunListItem(item)
		if !errors.Is(err, ErrInvalidRunListItem) {
			t.Errorf("ValidateRunListItem(%q) = %v, want ErrInvalidRunListItem", item, err)
			continue
		}
		if !strings.Contains(err.Error(), strconv.Quote(item)) {
			t.Errorf("ValidateRunListItem(%q) error %q does not name the item", item, err)
		}
	}
}
