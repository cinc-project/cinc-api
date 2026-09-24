package cinc

import (
	"slices"
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
