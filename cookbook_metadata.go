package cinc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrMetadataVersionNotLiteral reports a metadata.rb whose version is set by
// something other than a string literal (a method call, interpolation, a
// conditional). Its value can only be known by running the Ruby, which this
// package never does. ParseMetadataRb returns it, wrapped with the line,
// alongside the rest of the metadata, whose Version is left empty; without
// the check the cookbook would silently take Chef's default of 0.0.0.
var ErrMetadataVersionNotLiteral = errors.New("version is not a string literal, so it can't be read without running Ruby")

// LoadCookbookMetadata reads the cookbook metadata in dir. Like Chef's
// CookbookVersionLoader, metadata.json takes precedence over metadata.rb; see
// ParseMetadataJSON and ParseMetadataRb. With neither file the error wraps
// fs.ErrNotExist. As ParseMetadataRb does, it returns the metadata together
// with an error wrapping ErrMetadataVersionNotLiteral when metadata.rb
// computes its version.
//
// Values are returned as the file sets them: nothing is defaulted, so an
// unset Name or Version is empty (see CompiledJSON for Chef's defaults).
func LoadCookbookMetadata(dir string) (*CookbookMetadata, error) {
	for _, f := range []struct {
		name  string
		parse func([]byte, string) (*CookbookMetadata, error)
	}{{"metadata.json", parseMetadataJSON}, {"metadata.rb", parseMetadataRb}} {
		path := filepath.Join(dir, f.name)
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("cinc: read cookbook metadata: %w", err)
		}
		return f.parse(data, path)
	}
	return nil, fmt.Errorf("cinc: no metadata.json or metadata.rb in %s: %w", dir, fs.ErrNotExist)
}

// ParseMetadataJSON decodes a compiled metadata.json. Its dependencies are
// read the way Chef's Metadata#from_hash does (handle_incorrect_constraints):
// a legacy one-element constraint array ({"apt": [">= 1.0"]}) is unwrapped.
// Chef turns an empty or multi-element array into [], which the server
// rejects, so that is an error here. Every other value is kept as written,
// as from_hash keeps it.
func ParseMetadataJSON(data []byte) (*CookbookMetadata, error) {
	return parseMetadataJSON(data, "metadata.json")
}

func parseMetadataJSON(data []byte, file string) (*CookbookMetadata, error) {
	var raw struct {
		CookbookMetadata
		Dependencies map[string]any `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cinc: parse %s: %w", file, err)
	}
	md := raw.CookbookMetadata
	if raw.Dependencies != nil {
		md.Dependencies = make(map[string]string, len(raw.Dependencies))
	}
	for name, v := range raw.Dependencies {
		if list, ok := v.([]any); ok && len(list) == 1 {
			v = list[0]
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("cinc: parse %s: dependency %q: want one version constraint, got %v", file, name, v)
		}
		md.Dependencies[name] = s
	}
	return &md, nil
}

// metadataRbCall matches the start of a metadata.rb method call: the method
// name, then either "(" or whitespace before the arguments.
var metadataRbCall = regexp.MustCompile(`^[ \t]*([a-z_]+)(?:[ \t]*\(|[ \t]+)`)

// metadataRbAssignment matches a local variable assignment ("version = x"),
// which Ruby does not read as a call.
var metadataRbAssignment = regexp.MustCompile(`^[ \t]*[a-z_]+[ \t]*=[^=~]`)

// ParseMetadataRb statically extracts metadata from a metadata.rb. It is Ruby
// and is never evaluated, so only calls whose arguments are all literals are
// recognized, with the arguments Chef::Cookbook::Metadata's DSL accepts:
//
//	name, description, long_description, maintainer,
//	maintainer_email, license, source_url, issues_url   'string'
//	version                                             'x.y.z' | 'x.y'
//	depends, supports, provides                         'name'[, 'constraint']
//	chef_version, ohai_version                          'requirement'[, ...]
//	gem                                                 'name'[, 'requirement', ...]
//	recipe                                              'name', 'description'
//	privacy                                             true | false
//	eager_load_libraries                                true | false | 'glob' | ['glob', ...]
//
// Arguments may be parenthesized; strings may be single-quoted, or
// double-quoted without interpolation or escapes other than \" and \\; a
// symbol (:apt, :"apt") reads as its name; a # comment may end any line.
// A call may continue onto following lines after a trailing comma or an open
// parenthesis or bracket, as Ruby reads it (see metadataRbStatements).
//
// A statement whose arguments are not all literals (a computed value,
// interpolation, a trailing conditional) is skipped, as is any other method,
// which Chef's method_missing ignores too; metadata.json is the way to supply
// metadata this cannot see. The one exception is version: a version that is
// set but cannot be read returns the metadata parsed, with Version empty,
// and an error wrapping ErrMetadataVersionNotLiteral, since treating it as
// unset would publish the cookbook as 0.0.0. A later literal version call
// replaces a computed one, as it would in Ruby.
//
// Recognized values are normalized as Chef::Cookbook::Metadata stores them
// (version "1.2" is "1.2.0", constraint "1.2" is "= 1.2", requirement "16" is
// "= 16"), and a call Chef would raise on is an error: a malformed version or
// constraint, more than one constraint, a literal argument of the wrong type
// or number, or depends naming the cookbook itself (compared, as Chef does,
// with the name declared so far).
func ParseMetadataRb(data []byte) (*CookbookMetadata, error) {
	return parseMetadataRb(data, "metadata.rb")
}

func parseMetadataRb(data []byte, file string) (*CookbookMetadata, error) {
	var md CookbookMetadata
	computed := 0 // the line of a version call that could not be read
	for _, st := range metadataRbStatements(string(data)) {
		method, args, ok := parseRubyCall(st.text)
		if !ok {
			if m := metadataRbCall.FindStringSubmatch(st.text); m != nil && m[1] == "version" &&
				!metadataRbAssignment.MatchString(st.text) {
				computed, md.Version = st.line, ""
			}
			continue
		}
		if err := md.applyRb(method, args); err != nil {
			return nil, fmt.Errorf("cinc: parse %s: line %d: %s: %w", file, st.line, method, err)
		}
		if method == "version" {
			computed = 0
		}
	}
	if computed != 0 {
		return &md, fmt.Errorf("cinc: parse %s: line %d: %w", file, computed, ErrMetadataVersionNotLiteral)
	}
	return &md, nil
}

// metadataRbStatement is one statement of a metadata.rb, with any comments
// removed and continuation lines joined, and the line it starts on.
type metadataRbStatement struct {
	line int
	text string
}

// metadataRbStatements splits src into statements. A line continues onto
// the next when, ignoring its comment, it ends in ",", "(" or "[", or when a
// parenthesis or bracket it opened is still open and the next line starts
// with ")" or "]". A line that itself starts a call is never taken as a
// continuation, so a stray trailing comma or an unclosed parenthesis costs
// only its own statement, never the next call.
func metadataRbStatements(src string) []metadataRbStatement {
	lines := strings.Split(src, "\n")
	var out []metadataRbStatement
	for i := 0; i < len(lines); i++ {
		st := metadataRbStatement{line: i + 1}
		text, depth := rubyCode(lines[i])
		for i+1 < len(lines) && !metadataRbCall.MatchString(lines[i+1]) {
			text = strings.TrimRight(text, " \t\r")
			next := strings.TrimLeft(lines[i+1], " \t")
			open := strings.HasSuffix(text, ",") || strings.HasSuffix(text, "(") || strings.HasSuffix(text, "[")
			closing := depth > 0 && (strings.HasPrefix(next, ")") || strings.HasPrefix(next, "]"))
			if !open && !closing {
				break
			}
			i++
			code, d := rubyCode(next)
			text += " " + code
			depth += d
		}
		st.text = text
		out = append(out, st)
	}
	return out
}

// rubyCode returns line without its trailing # comment, and how many more
// parentheses and brackets it opens than closes, both ignoring anything
// inside a quoted string. An unterminated string leaves the rest of the line
// as code.
func rubyCode(line string) (code string, depth int) {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0 && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#':
			return line[:i], depth
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		}
	}
	return line, depth
}

// rubyArgKind is the type of a literal metadata.rb argument.
type rubyArgKind int

const (
	rubyString rubyArgKind = iota // a string or symbol
	rubyBool                      // true or false
	rubyList                      // an array of strings or symbols
)

// rubyArg is one literal argument.
type rubyArg struct {
	kind rubyArgKind
	s    string   // rubyString
	b    bool     // rubyBool
	list []string // rubyList
}

// applyRb records one recognized metadata.rb call, enforcing the argument
// types and counts Chef::Cookbook::Metadata's DSL does. Methods it does not
// know are ignored, as Chef's method_missing ignores them.
func (md *CookbookMetadata) applyRb(method string, args []rubyArg) error {
	if field := md.stringField(method); field != nil {
		s, err := oneString(args)
		if err != nil {
			return err
		}
		*field = s
		return nil
	}
	switch method {
	case "version":
		s, err := oneString(args)
		if err != nil {
			return err
		}
		v, err := chefVersion(s)
		if err != nil {
			return err
		}
		md.Version = v
	case "privacy":
		if len(args) != 1 || args[0].kind != rubyBool {
			return errors.New("want true or false")
		}
		md.Privacy = args[0].b
	case "eager_load_libraries":
		if len(args) != 1 {
			return errors.New("want one argument")
		}
		switch a := args[0]; a.kind {
		case rubyBool:
			md.EagerLoadLibraries = a.b
		case rubyString:
			md.EagerLoadLibraries = a.s
		default:
			md.EagerLoadLibraries = a.list
		}
	case "depends", "supports", "provides":
		strs, err := stringArgs(args)
		if err != nil {
			return err
		}
		if method == "depends" && md.Name != "" && strs[0] == md.Name {
			return fmt.Errorf("cookbook %q depends on itself", md.Name)
		}
		// Chef's new_args_format: one optional constraint, default ">= 0.0.0".
		constraint := ">= 0.0.0"
		switch len(strs) {
		case 1:
		case 2:
			c, err := chefConstraint(strs[1])
			if err != nil {
				return err
			}
			constraint = c
		default:
			return fmt.Errorf("%q: only one version constraint is allowed", strs[0])
		}
		m := &md.Dependencies
		switch method {
		case "supports":
			m = &md.Platforms
		case "provides":
			m = &md.Providing
		}
		if *m == nil {
			*m = map[string]string{}
		}
		(*m)[strs[0]] = constraint
	case "chef_version", "ohai_version":
		strs, err := stringArgs(args)
		if err != nil {
			return err
		}
		reqs, err := gemRequirements(strs)
		if err != nil {
			return err
		}
		if method == "chef_version" {
			md.ChefVersions = append(md.ChefVersions, reqs)
		} else {
			md.OhaiVersions = append(md.OhaiVersions, reqs)
		}
	case "gem":
		// Chef keeps gem's arguments as given; chef-client hands them to
		// bundler.
		strs, err := stringArgs(args)
		if err != nil {
			return err
		}
		md.Gems = append(md.Gems, strs)
	case "recipe":
		strs, err := stringArgs(args)
		if err != nil {
			return err
		}
		if len(strs) != 2 {
			return errors.New("want a recipe name and a description")
		}
		if md.Recipes == nil {
			md.Recipes = map[string]string{}
		}
		md.Recipes[strs[0]] = strs[1]
	}
	return nil
}

// oneString returns the single string argument of a call such as name or
// version, which Chef's set_or_return validates as kind_of String.
func oneString(args []rubyArg) (string, error) {
	if len(args) != 1 || args[0].kind != rubyString {
		return "", errors.New("want one string argument")
	}
	return args[0].s, nil
}

// stringArgs returns the arguments of a call that takes only strings.
// parseRubyCall yields at least one argument, so the result is non-empty.
func stringArgs(args []rubyArg) ([]string, error) {
	strs := make([]string, len(args))
	for i, a := range args {
		if a.kind != rubyString {
			return nil, errors.New("want string arguments")
		}
		strs[i] = a.s
	}
	return strs, nil
}

// stringField returns the plain string field a metadata.rb method sets, or nil.
func (md *CookbookMetadata) stringField(method string) *string {
	switch method {
	case "name":
		return &md.Name
	case "description":
		return &md.Description
	case "long_description":
		return &md.LongDescription
	case "maintainer":
		return &md.Maintainer
	case "maintainer_email":
		return &md.MaintainerEmail
	case "license":
		return &md.License
	case "source_url":
		return &md.SourceURL
	case "issues_url":
		return &md.IssuesURL
	}
	return nil
}

// parseRubyCall parses a statement holding a single method call whose
// arguments are all literals (see ParseMetadataRb). ok is false for anything
// else.
func parseRubyCall(line string) (method string, args []rubyArg, ok bool) {
	m := metadataRbCall.FindStringSubmatchIndex(line)
	if m == nil {
		return "", nil, false
	}
	method = line[m[2]:m[3]]
	paren := strings.HasSuffix(line[:m[1]], "(")
	rest := line[m[1]:]
	for {
		rest = strings.TrimLeft(rest, " \t")
		arg, tail, ok := parseRubyLiteral(rest)
		if !ok {
			return "", nil, false
		}
		args = append(args, arg)
		rest = strings.TrimLeft(tail, " \t")
		if !strings.HasPrefix(rest, ",") {
			break
		}
		rest = rest[1:]
	}
	if paren {
		if !strings.HasPrefix(rest, ")") {
			return "", nil, false
		}
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	rest = strings.TrimRight(rest, " \t\r")
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", nil, false
	}
	return method, args, true
}

// parseRubyLiteral reads one literal from the start of s: a single- or
// double-quoted string, a symbol, the bare word true/false, or an array of
// strings and symbols.
func parseRubyLiteral(s string) (arg rubyArg, rest string, ok bool) {
	for _, word := range []string{"true", "false"} {
		if after, found := strings.CutPrefix(s, word); found && !startsIdent(after) {
			return rubyArg{kind: rubyBool, b: word == "true"}, after, true
		}
	}
	if strings.HasPrefix(s, ":") {
		return parseRubySymbol(s[1:])
	}
	if strings.HasPrefix(s, "[") {
		return parseRubyList(s[1:])
	}
	return parseRubyString(s)
}

// parseRubyString reads a single- or double-quoted string from the start of s.
func parseRubyString(s string) (arg rubyArg, rest string, ok bool) {
	if s == "" || (s[0] != '\'' && s[0] != '"') {
		return rubyArg{}, "", false
	}
	quote := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == quote:
			return rubyArg{s: b.String()}, s[i+1:], true
		case c == '\\':
			// Single quotes only escape \' and \\ (any other backslash is
			// literal); double quotes have many escapes, of which only \" and
			// \\ are taken.
			if i+1 < len(s) && (s[i+1] == quote || s[i+1] == '\\') {
				i++
				b.WriteByte(s[i])
			} else if quote == '\'' {
				b.WriteByte(c)
			} else {
				return rubyArg{}, "", false
			}
		case quote == '"' && c == '#' && i+1 < len(s) && s[i+1] == '{':
			return rubyArg{}, "", false // interpolation
		default:
			b.WriteByte(c)
		}
	}
	return rubyArg{}, "", false // unterminated
}

// parseRubyList reads an array of strings and symbols from s, the text after
// its "[". A trailing comma before the "]" is allowed, as in Ruby.
func parseRubyList(s string) (arg rubyArg, rest string, ok bool) {
	list := []string{}
	for {
		s = strings.TrimLeft(s, " \t")
		if after, found := strings.CutPrefix(s, "]"); found {
			return rubyArg{kind: rubyList, list: list}, after, true
		}
		elem, tail, ok := parseRubyLiteral(s)
		if !ok || elem.kind != rubyString {
			return rubyArg{}, "", false
		}
		list = append(list, elem.s)
		s = strings.TrimLeft(tail, " \t")
		if after, found := strings.CutPrefix(s, ","); found {
			s = after
		} else if !strings.HasPrefix(s, "]") {
			return rubyArg{}, "", false
		}
	}
}

// parseRubySymbol reads a symbol's name from s, the text after its ":":
// a quoted string (:"build-essential") or an identifier (:apt). Chef keys
// the value by the symbol, which serializes as its name.
func parseRubySymbol(s string) (arg rubyArg, rest string, ok bool) {
	if s != "" && (s[0] == '\'' || s[0] == '"') {
		return parseRubyString(s)
	}
	n := 0
	for n < len(s) && startsIdent(s[n:]) {
		n++
	}
	if n == 0 || s[0] >= '0' && s[0] <= '9' {
		return rubyArg{}, "", false
	}
	return rubyArg{s: s[:n]}, s[n:], true
}

// startsIdent reports whether s begins with a Ruby identifier character, so
// that "trueish" is not read as the literal true.
func startsIdent(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// chefVersionRe is Chef::Version's accepted syntax: x.y.z or x.y.
var chefVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?$`)

// chefVersion normalizes a cookbook version the way Chef::Version#to_s does:
// "1.2" becomes "1.2.0" and leading zeros are dropped.
func chefVersion(s string) (string, error) {
	v, err := parseChefVersion(s)
	if err != nil {
		return "", err
	}
	parts := make([]string, 3)
	for i, n := range v {
		parts[i] = strconv.FormatUint(n, 10)
	}
	return strings.Join(parts, "."), nil
}

// parseChefVersion parses a cookbook version into its major, minor and patch
// numbers, a missing patch being zero.
func parseChefVersion(s string) ([3]uint64, error) {
	var v [3]uint64
	m := chefVersionRe.FindStringSubmatch(s)
	if m == nil {
		return v, fmt.Errorf("version %q does not match 'x.y.z' or 'x.y'", s)
	}
	for i, p := range m[1:] {
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 63)
		if err != nil {
			return v, fmt.Errorf("version %q: %w", s, err)
		}
		v[i] = n
	}
	return v, nil
}

// chefConstraintRe is Chef::VersionConstraint::PATTERN.
var chefConstraintRe = regexp.MustCompile(`^(<=|>=|~>|<|>|=) *([0-9].*)$`)

// platformVersionRe is Chef::Version::Platform's accepted syntax: x.y.z, x.y,
// x, or FreeBSD's x.y-RELEASE[-pN] (whose "." Chef leaves unescaped).
var platformVersionRe = regexp.MustCompile(`^\d+(?:\.\d+(?:\.\d+)?)?$|(?i)^\d+.\d+-[a-z]+\d?(?:-p\d+)?$`)

// chefConstraint normalizes a depends/supports/provides constraint the way
// Chef::VersionConstraint#to_s does: "OP VERSION", with a lone version
// meaning "= VERSION" and the version kept as written. Chef's metadata
// validates every such constraint as a Chef::VersionConstraint::Platform, so
// the version must be a valid Chef::Version::Platform.
func chefConstraint(s string) (string, error) {
	op, v := "=", s
	if strings.Contains(s, " ") || s == "" || s[0] < '0' || s[0] > '9' {
		m := chefConstraintRe.FindStringSubmatch(s)
		if m == nil {
			return "", fmt.Errorf("invalid version constraint %q", s)
		}
		op, v = m[1], m[2]
	}
	if !platformVersionRe.MatchString(v) {
		return "", fmt.Errorf("invalid version constraint %q", s)
	}
	return op + " " + v, nil
}

// gemRequirementRe is Gem::Requirement::PATTERN, loosened only in allowing
// the version to be anything that starts with a digit (as Gem::Version does).
var gemRequirementRe = regexp.MustCompile(`^\s*(=|!=|>=|<=|>|<|~>)?\s*([0-9][0-9A-Za-z.\-]*)\s*$`)

// gemRequirements normalizes one chef_version/ohai_version call's arguments
// as Chef's gem_requirements_to_array serializes the Gem::Dependency it
// builds: repeated arguments dropped (Gem::Requirement#initialize), each in
// "OP VERSION" form with the operator defaulting to "=", then sorted.
func gemRequirements(args []string) ([]string, error) {
	reqs := make([]string, 0, len(args))
	for i, s := range args {
		if slices.Contains(args[:i], s) {
			continue
		}
		m := gemRequirementRe.FindStringSubmatch(s)
		if m == nil {
			return nil, fmt.Errorf("invalid requirement %q", s)
		}
		op := m[1]
		if op == "" {
			op = "="
		}
		reqs = append(reqs, op+" "+m[2])
	}
	slices.Sort(reqs)
	return reqs, nil
}
