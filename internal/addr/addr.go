// Package addr parses Terraform resource addresses and matches exclude/target
// patterns against them.
//
// A pattern with no glob metacharacter follows OpenTofu -exclude/-target
// addressing: a module address selects everything inside that module (and its
// child modules), a resource address without an instance key selects every
// instance, and an instance key selects that instance only. An omitted module
// instance key matches every instance of that module.
//
// A pattern containing "*" is a glob. "[*]" matches one instance key ([0] or
// ["name"]). "*" matches any sequence of characters. A "." in a glob also
// allows an instance key before that dot, so module.legacy.* matches both
// module.legacy.aws_instance.db and module.legacy[0].aws_instance.db.
package addr

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Index is a count or for_each instance key.
type Index struct {
	// Num is set when the key is a decimal integer ([0], [12]).
	Num *int
	// Str is set when the key is a quoted string (["k"], ["a.b"]).
	Str *string
}

func (i Index) String() string {
	if i.Num != nil {
		return fmt.Sprintf("[%d]", *i.Num)
	}
	if i.Str != nil {
		return `["` + escapeString(*i.Str) + `"]`
	}
	return ""
}

func (i Index) equal(o Index) bool {
	switch {
	case i.Num != nil && o.Num != nil:
		return *i.Num == *o.Num
	case i.Str != nil && o.Str != nil:
		return *i.Str == *o.Str
	default:
		return false
	}
}

// Module is one module call in an address path.
type Module struct {
	Name  string
	Index *Index
}

// Address is a managed resource address or a module address (no resource).
type Address struct {
	Modules     []Module
	Type        string
	Name        string
	Instance    *Index
	HasResource bool
}

// Key is the configuration address with every instance key removed.
// module.instances[0].terraform_data.leaf and module.instances[1].terraform_data.leaf
// share the key module.instances.terraform_data.leaf, which is how terraform
// graph names the resource.
func (a Address) Key() string {
	if !a.HasResource {
		return ""
	}
	var b strings.Builder
	for _, m := range a.Modules {
		b.WriteString("module.")
		b.WriteString(m.Name)
		b.WriteByte('.')
	}
	b.WriteString(a.Type)
	b.WriteByte('.')
	b.WriteString(a.Name)
	return b.String()
}

func (a Address) String() string {
	var b strings.Builder
	for _, m := range a.Modules {
		b.WriteString("module.")
		b.WriteString(m.Name)
		if m.Index != nil {
			b.WriteString(m.Index.String())
		}
		b.WriteByte('.')
	}
	if !a.HasResource {
		return strings.TrimSuffix(b.String(), ".")
	}
	b.WriteString(a.Type)
	b.WriteByte('.')
	b.WriteString(a.Name)
	if a.Instance != nil {
		b.WriteString(a.Instance.String())
	}
	return b.String()
}

// Pattern is either a parsed address (OpenTofu rules) or a compiled glob.
type Pattern struct {
	Raw    string
	addr   Address
	glob   *regexp.Regexp
	isGlob bool
}

// IsGlob reports whether the pattern uses * or [*].
func (p Pattern) IsGlob() bool { return p.isGlob }

func (p Pattern) String() string { return p.Raw }

// Compile parses a user pattern. It does not check whether anything matches.
func Compile(raw string) (Pattern, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Pattern{}, fmt.Errorf("empty address pattern")
	}
	if strings.Contains(raw, "*") {
		re, err := compileGlob(raw)
		if err != nil {
			return Pattern{}, err
		}
		return Pattern{Raw: raw, glob: re, isGlob: true}, nil
	}
	a, err := Parse(raw)
	if err != nil {
		return Pattern{}, fmt.Errorf("parse address %q: %w", raw, err)
	}
	return Pattern{Raw: raw, addr: a}, nil
}

// Match reports whether concrete resource address s is selected by the pattern.
func (p Pattern) Match(s string) bool {
	if p.isGlob {
		return p.glob.MatchString(s)
	}
	a, err := Parse(s)
	if err != nil || !a.HasResource {
		return false
	}
	return addressMatch(p.addr, a)
}

// Parse parses a module address or a managed resource address.
// Instance keys and module indexes are preserved.
func Parse(s string) (Address, error) {
	rest := s
	var a Address
	for strings.HasPrefix(rest, "module.") {
		rest = rest[len("module."):]
		name, next, err := parseIdent(rest)
		if err != nil {
			return Address{}, fmt.Errorf("parse %q: %w", s, err)
		}
		idx, next, err := parseOptionalIndex(next)
		if err != nil {
			return Address{}, fmt.Errorf("parse %q: %w", s, err)
		}
		a.Modules = append(a.Modules, Module{Name: name, Index: idx})
		rest = next
		if rest == "" {
			return a, nil
		}
		if !strings.HasPrefix(rest, ".") {
			return Address{}, fmt.Errorf("parse %q: expected '.' after module %s", s, name)
		}
		rest = rest[1:]
	}
	if rest == "" {
		return Address{}, fmt.Errorf("parse %q: missing resource", s)
	}
	typ, next, err := parseIdent(rest)
	if err != nil {
		return Address{}, fmt.Errorf("parse %q: %w", s, err)
	}
	if !strings.HasPrefix(next, ".") {
		return Address{}, fmt.Errorf("parse %q: expected type.name", s)
	}
	next = next[1:]
	name, next, err := parseIdent(next)
	if err != nil {
		return Address{}, fmt.Errorf("parse %q: %w", s, err)
	}
	idx, next, err := parseOptionalIndex(next)
	if err != nil {
		return Address{}, fmt.Errorf("parse %q: %w", s, err)
	}
	if next != "" {
		return Address{}, fmt.Errorf("parse %q: trailing %q", s, next)
	}
	a.Type = typ
	a.Name = name
	a.Instance = idx
	a.HasResource = true
	return a, nil
}

// IsGraphResource reports whether a terraform graph node name (already
// normalized, no [root] prefix and no (expand)/(destroy) suffix) is a managed
// resource configuration address. Locals, variables, outputs, and providers
// are not.
func IsGraphResource(node string) bool {
	a, err := Parse(node)
	if err != nil || !a.HasResource {
		return false
	}
	switch a.Type {
	case "var", "local", "output", "path", "terraform", "module", "each", "count", "self", "data":
		return false
	default:
		return true
	}
}

func addressMatch(pat, addr Address) bool {
	if len(pat.Modules) > len(addr.Modules) {
		return false
	}
	if pat.HasResource && len(pat.Modules) != len(addr.Modules) {
		return false
	}
	for i := range pat.Modules {
		if pat.Modules[i].Name != addr.Modules[i].Name {
			return false
		}
		if pat.Modules[i].Index != nil {
			if addr.Modules[i].Index == nil || !pat.Modules[i].Index.equal(*addr.Modules[i].Index) {
				return false
			}
		}
	}
	if !pat.HasResource {
		return addr.HasResource
	}
	if pat.Type != addr.Type || pat.Name != addr.Name {
		return false
	}
	if pat.Instance != nil {
		if addr.Instance == nil || !pat.Instance.equal(*addr.Instance) {
			return false
		}
	}
	return true
}

func parseIdent(s string) (string, string, error) {
	if s == "" {
		return "", "", fmt.Errorf("expected identifier")
	}
	r, w := utf8.DecodeRuneInString(s)
	if !isIdentStart(r) {
		return "", "", fmt.Errorf("expected identifier, found %q", s)
	}
	i := w
	for i < len(s) {
		r, w = utf8.DecodeRuneInString(s[i:])
		if !isIdentCont(r) {
			break
		}
		i += w
	}
	return s[:i], s[i:], nil
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isIdentCont(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9') || r == '-'
}

func parseOptionalIndex(s string) (*Index, string, error) {
	if !strings.HasPrefix(s, "[") {
		return nil, s, nil
	}
	idx, rest, err := parseIndex(s)
	if err != nil {
		return nil, "", err
	}
	return &idx, rest, nil
}

func parseIndex(s string) (Index, string, error) {
	if !strings.HasPrefix(s, "[") {
		return Index{}, "", fmt.Errorf("expected index")
	}
	if strings.HasPrefix(s, `["`) {
		i := 2
		var b strings.Builder
		for i < len(s) {
			if s[i] == '\\' {
				if i+1 >= len(s) {
					return Index{}, "", fmt.Errorf("truncated escape in index")
				}
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			if s[i] == '"' {
				if i+1 >= len(s) || s[i+1] != ']' {
					return Index{}, "", fmt.Errorf("unterminated string index")
				}
				str := b.String()
				return Index{Str: &str}, s[i+2:], nil
			}
			b.WriteByte(s[i])
			i++
		}
		return Index{}, "", fmt.Errorf("unterminated string index")
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return Index{}, "", fmt.Errorf("unterminated index")
	}
	body := s[1:end]
	n, err := strconv.Atoi(body)
	if err != nil || (len(body) > 1 && body[0] == '0') || strings.HasPrefix(body, "+") || strings.HasPrefix(body, "-") {
		return Index{}, "", fmt.Errorf("invalid numeric index %q", body)
	}
	return Index{Num: &n}, s[end+1:], nil
}

func escapeString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

const indexRE = `(?:\[[0-9]+\]|\["(?:\\.|[^"\\])*"\])`

func compileGlob(pat string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pat); {
		if strings.HasPrefix(pat[i:], "[*]") {
			b.WriteString(indexRE)
			i += 3
			continue
		}
		switch pat[i] {
		case '*':
			b.WriteString(".*")
			i++
		case '.':
			// An instance key may sit between a name and the next dot:
			// module.legacy[0].resource matches module.legacy.*
			b.WriteString("(?:" + indexRE + `)?\.`)
			i++
		default:
			b.WriteString(regexp.QuoteMeta(pat[i : i+1]))
			i++
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("compile glob %q: %w", pat, err)
	}
	return re, nil
}
