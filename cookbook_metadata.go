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

// loadCookbookMetadata reads dir's cookbook metadata. Like Chef's cookbook
// loader, metadata.json takes precedence over metadata.rb; with neither, the
// zero value is returned.
func loadCookbookMetadata(dir string) (CookbookMetadata, error) {
	for _, f := range []struct {
		name  string
		parse func([]byte) (CookbookMetadata, error)
	}{{"metadata.json", parseMetadataJSON}, {"metadata.rb", parseMetadataRb}} {
		data, err := os.ReadFile(filepath.Join(dir, f.name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return CookbookMetadata{}, err
		}
		return f.parse(data)
	}
	return CookbookMetadata{}, nil
}

// parseMetadataJSON decodes a compiled metadata.json. Its dependencies are
// read the way Chef's Metadata#from_hash does (handle_incorrect_constraints):
// a legacy one-element constraint array is unwrapped. Chef turns an empty or
// multi-element array into [], which the server rejects, so that is an error
// here.
func parseMetadataJSON(data []byte) (CookbookMetadata, error) {
	var raw struct {
		CookbookMetadata
		Dependencies map[string]any `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CookbookMetadata{}, fmt.Errorf("metadata.json: %w", err)
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
			return CookbookMetadata{}, fmt.Errorf("metadata.json: dependency %q: want one version constraint, got %v", name, v)
		}
		md.Dependencies[name] = s
	}
	return md, nil
}

// metadataRbCall matches the start of a metadata.rb method call: the method
// name, then either "(" or whitespace before the arguments.
var metadataRbCall = regexp.MustCompile(`^[ \t]*([a-z_]+)(?:[ \t]*\(|[ \t]+)`)

// parseMetadataRb statically extracts metadata from a metadata.rb. It is Ruby
// and is never evaluated, so only calls whose arguments are all literals are
// recognized:
//
//	name, version, description, long_description, maintainer,
//	maintainer_email, license, source_url, issues_url   'string'
//	depends, supports                                     'name'[, 'constraint']
//	chef_version, ohai_version                            'constraint'[, ...]
//	privacy                                               true | false
//
// Arguments may be parenthesized; strings may be single-quoted, or
// double-quoted without interpolation or escapes other than \" and \\; a
// symbol (:apt, :"apt") reads as its name; a # comment may end any line.
// A call may continue onto following lines after a trailing comma or an
// open parenthesis, as Ruby reads it (see metadataRbStatements). Any other
// statement — a computed value, interpolation, a conditional, another
// method — is skipped, so metadata.json is the way to supply metadata this
// cannot see.
//
// Recognized values are normalized as Chef::Cookbook::Metadata stores them,
// and values Chef would reject (a malformed version or constraint, a cookbook
// depending on itself) are errors.
func parseMetadataRb(data []byte) (CookbookMetadata, error) {
	var md CookbookMetadata
	for _, st := range metadataRbStatements(string(data)) {
		method, args, ok := parseRubyCall(st.text)
		if !ok {
			continue
		}
		if err := md.applyRb(method, args); err != nil {
			return CookbookMetadata{}, fmt.Errorf("metadata.rb:%d: %s: %w", st.line, method, err)
		}
	}
	if _, ok := md.Dependencies[md.Name]; ok && md.Name != "" {
		return CookbookMetadata{}, fmt.Errorf("metadata.rb: cookbook %q depends on itself", md.Name)
	}
	return md, nil
}

// metadataRbStatement is one statement of a metadata.rb, with any comments
// removed and continuation lines joined, and the line it starts on.
type metadataRbStatement struct {
	line int
	text string
}

// metadataRbStatements splits src into statements. A line continues onto
// the next when, ignoring its comment, it ends in "," or "(", or when a
// parenthesis it opened is still open and the next line starts with ")".
// A line that itself starts a call is never taken as a continuation, so a
// stray trailing comma or an unclosed parenthesis costs only its own
// statement, never the next call.
func metadataRbStatements(src string) []metadataRbStatement {
	lines := strings.Split(src, "\n")
	var out []metadataRbStatement
	for i := 0; i < len(lines); i++ {
		st := metadataRbStatement{line: i + 1}
		text, depth := rubyCode(lines[i])
		for i+1 < len(lines) && !metadataRbCall.MatchString(lines[i+1]) {
			text = strings.TrimRight(text, " \t\r")
			next := strings.TrimLeft(lines[i+1], " \t")
			if !strings.HasSuffix(text, ",") && !strings.HasSuffix(text, "(") &&
				(depth <= 0 || !strings.HasPrefix(next, ")")) {
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
// parentheses it opens than closes, both ignoring anything inside a quoted
// string. An unterminated string leaves the rest of the line as code.
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
		case c == '(':
			depth++
		case c == ')':
			depth--
		}
	}
	return line, depth
}

// rubyArg is one literal argument: a string, or the bare word true/false.
type rubyArg struct {
	s    string
	bare bool
}

// applyRb records one recognized metadata.rb call. Calls with an argument
// shape Chef's DSL would not accept for that method are ignored.
func (md *CookbookMetadata) applyRb(method string, args []rubyArg) error {
	strs := make([]string, 0, len(args))
	for _, a := range args {
		if a.bare {
			if method == "privacy" && len(args) == 1 {
				md.Privacy = a.s == "true"
			}
			return nil
		}
		strs = append(strs, a.s)
	}
	// parseRubyCall yields at least one argument, so strs is non-empty.
	if field := md.stringField(method); field != nil {
		if len(strs) == 1 {
			*field = strs[0]
		}
		return nil
	}
	switch method {
	case "version":
		if len(strs) != 1 {
			return nil
		}
		v, err := chefVersion(strs[0])
		if err != nil {
			return err
		}
		md.Version = v
	case "depends", "supports":
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
		if method == "supports" {
			m = &md.Platforms
		}
		if *m == nil {
			*m = map[string]string{}
		}
		(*m)[strs[0]] = constraint
	case "chef_version", "ohai_version":
		reqs := make([]string, len(strs))
		for i, s := range strs {
			r, err := gemRequirement(s)
			if err != nil {
				return err
			}
			reqs[i] = r
		}
		slices.Sort(reqs) // gem_requirements_to_array sorts each list
		if method == "chef_version" {
			md.ChefVersions = append(md.ChefVersions, reqs)
		} else {
			md.OhaiVersions = append(md.OhaiVersions, reqs)
		}
	}
	return nil
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

// parseRubyCall parses a line holding a single method call whose arguments
// are all literals (see parseMetadataRb). ok is false for anything else.
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
// double-quoted string, or the bare word true/false.
func parseRubyLiteral(s string) (arg rubyArg, rest string, ok bool) {
	for _, word := range []string{"true", "false"} {
		if after, found := strings.CutPrefix(s, word); found && !startsIdent(after) {
			return rubyArg{s: word, bare: true}, after, true
		}
	}
	if strings.HasPrefix(s, ":") {
		return parseRubySymbol(s[1:])
	}
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

// parseRubySymbol reads a symbol's name from s, the text after its ":":
// a quoted string (:"build-essential") or an identifier (:apt). Chef keys
// the value by the symbol, which serializes as its name.
func parseRubySymbol(s string) (arg rubyArg, rest string, ok bool) {
	if s != "" && (s[0] == '\'' || s[0] == '"') {
		return parseRubyLiteral(s)
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
	m := chefVersionRe.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("version %q does not match 'x.y.z' or 'x.y'", s)
	}
	parts := make([]string, 3)
	for i, p := range m[1:] {
		if p == "" {
			p = "0"
		}
		n, err := strconv.ParseUint(p, 10, 63)
		if err != nil {
			return "", fmt.Errorf("version %q: %w", s, err)
		}
		parts[i] = strconv.FormatUint(n, 10)
	}
	return strings.Join(parts, "."), nil
}

// chefConstraintRe is Chef::VersionConstraint::PATTERN.
var chefConstraintRe = regexp.MustCompile(`^(<=|>=|~>|<|>|=) *([0-9].*)$`)

// chefConstraint normalizes a cookbook version constraint the way
// Chef::VersionConstraint#to_s does: "OP VERSION", with a lone version
// meaning "= VERSION". The version itself must be a valid Chef::Version.
func chefConstraint(s string) (string, error) {
	op, v := "=", s
	if strings.Contains(s, " ") || s == "" || s[0] < '0' || s[0] > '9' {
		m := chefConstraintRe.FindStringSubmatch(s)
		if m == nil {
			return "", fmt.Errorf("invalid version constraint %q", s)
		}
		op, v = m[1], m[2]
	}
	if !chefVersionRe.MatchString(v) {
		return "", fmt.Errorf("invalid version constraint %q", s)
	}
	return op + " " + v, nil
}

// gemRequirementRe is Gem::Requirement::PATTERN, loosened only in allowing
// the version to be anything that starts with a digit (as Gem::Version does).
var gemRequirementRe = regexp.MustCompile(`^\s*(=|!=|>=|<=|>|<|~>)?\s*([0-9][0-9A-Za-z.\-]*)\s*$`)

// gemRequirement normalizes one chef_version/ohai_version requirement to
// Gem::Requirement's "OP VERSION" form, defaulting the operator to "=".
func gemRequirement(s string) (string, error) {
	m := gemRequirementRe.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("invalid requirement %q", s)
	}
	op := m[1]
	if op == "" {
		op = "="
	}
	return op + " " + m[2], nil
}
