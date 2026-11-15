package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultMode    = "copy"
	defaultBranch  = "main"
	defaultMessage = "Update from private repo"
)

// Settings holds the defaults applied to every publish entry.
type Settings struct {
	Owner      string
	Mode       string
	Branch     string
	Message    string
	SecretScan bool
}

// Entry describes a published file or folder; empty Mode and Message mean "inherit from Settings".
type Entry struct {
	Name        string
	Path        string
	Description string
	Include     []string
	Exclude     []string
	Message     string
	Mode        string
}

// Config is a parsed outlet.yml file with Entries in file order.
type Config struct {
	Settings Settings
	Entries  []Entry
}

// Problem is one structural error found in a config file.
type Problem struct {
	Line    int
	Message string
}

// String renders the problem as "line N: message".
func (p Problem) String() string {
	return fmt.Sprintf("line %d: %s", p.Line, p.Message)
}

// Error reports every structural problem found in one config file.
type Error struct {
	Path     string
	Problems []Problem
}

// Error lists each problem prefixed with the config path and line.
func (e *Error) Error() string {
	if len(e.Problems) == 0 {
		return e.Path
	}

	lines := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		lines = append(lines, fmt.Sprintf("%s:%d: %s", e.Path, p.Line, p.Message))
	}

	return strings.Join(lines, "\n")
}

// Load reads path, parses outlet.yml and applies the config defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg, err := parse(path, data)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// UnmarshalYAML accepts a path string or a full entry mapping.
func (e *Entry) UnmarshalYAML(n *yaml.Node) error {
	p := &parser{}

	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag != "!!str" {
			p.add(n, "expected path string or mapping, got %s", kindName(n))
			break
		}
		e.Path = n.Value
	case yaml.MappingNode:
		p.entry(n, e)
	default:
		p.add(n, "expected path string or mapping, got %s", kindName(n))
	}

	if len(p.problems) > 0 {
		return problemsError(p.problems)
	}

	return nil
}

func parse(path string, data []byte) (*Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	cfg := &Config{Settings: Settings{SecretScan: true}}
	p := &parser{}
	p.document(&doc, cfg)
	applyDefaults(&cfg.Settings)

	if len(p.problems) > 0 {
		return nil, &Error{Path: path, Problems: p.problems}
	}

	return cfg, nil
}

func applyDefaults(s *Settings) {
	if s.Mode == "" {
		s.Mode = defaultMode
	}
	if s.Branch == "" {
		s.Branch = defaultBranch
	}
	if s.Message == "" {
		s.Message = defaultMessage
	}
}

type parser struct {
	problems []Problem
}

func (p *parser) add(n *yaml.Node, format string, args ...any) {
	p.problems = append(p.problems, Problem{Line: n.Line, Message: fmt.Sprintf(format, args...)})
}

func (p *parser) document(doc *yaml.Node, cfg *Config) {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return
		}
		root = root.Content[0]
	}
	if root.Kind == 0 {
		return
	}
	if root.Kind != yaml.MappingNode {
		p.add(root, "top level must be a mapping")
		return
	}

	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch key.Value {
		case "settings":
			p.settings(value, &cfg.Settings)
		case "publish":
			cfg.Entries = p.publish(value)
		default:
			p.add(key, "unknown key %q at top level", key.Value)
		}
	}
}

func (p *parser) settings(n *yaml.Node, s *Settings) {
	if n.Kind != yaml.MappingNode {
		p.add(n, "settings must be a mapping")
		return
	}

	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		switch key.Value {
		case "owner":
			s.Owner = p.string(key, value)
		case "mode":
			s.Mode = p.string(key, value)
			if s.Mode != "" && !validMode(s.Mode) {
				p.add(value, "settings: invalid mode %q: must be copy or subtree", s.Mode)
			}
		case "branch":
			s.Branch = p.string(key, value)
		case "message":
			s.Message = p.string(key, value)
		case "secret_scan":
			if b, ok := p.boolean(key, value); ok {
				s.SecretScan = b
			}
		default:
			p.add(key, "unknown key %q in settings", key.Value)
		}
	}
}

func (p *parser) publish(n *yaml.Node) []Entry {
	if n.Kind != yaml.MappingNode {
		p.add(n, "publish must be a mapping of names to entries")
		return nil
	}

	entries := make([]Entry, 0, len(n.Content)/2)
	seen := make(map[string]int, len(n.Content)/2)

	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		name := key.Value

		if first, ok := seen[name]; ok {
			p.add(key, "duplicate publish name %q (first defined on line %d)", name, first)
			continue
		}
		seen[name] = key.Line

		var entry Entry
		if err := value.Decode(&entry); err != nil {
			p.collect(value, name, err)
			continue
		}

		entry.Name = name
		if entry.Path == "" {
			p.add(key, "entry %q: missing path", name)
			continue
		}

		entries = append(entries, entry)
	}

	return entries
}

func (p *parser) collect(n *yaml.Node, name string, err error) {
	var problems problemsError
	if errors.As(err, &problems) {
		for _, problem := range problems {
			p.problems = append(p.problems, Problem{
				Line:    problem.Line,
				Message: fmt.Sprintf("entry %q: %s", name, problem.Message),
			})
		}
		return
	}

	p.add(n, "entry %q: %v", name, err)
}

// NOTE: KnownFields does not reach UnmarshalYAML, so entry keys are checked here.
func (p *parser) entry(n *yaml.Node, e *Entry) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		switch key.Value {
		case "path":
			e.Path = p.string(key, value)
		case "description":
			e.Description = p.string(key, value)
		case "include":
			e.Include = p.list(key, value)
		case "exclude":
			e.Exclude = p.list(key, value)
		case "message":
			e.Message = p.string(key, value)
		case "mode":
			e.Mode = p.string(key, value)
			if e.Mode != "" && !validMode(e.Mode) {
				p.add(value, "invalid mode %q: must be copy or subtree", e.Mode)
			}
		default:
			p.add(key, "unknown key %q", key.Value)
		}
	}
}

func (p *parser) string(key, value *yaml.Node) string {
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		p.add(value, "%s: expected string, got %s", key.Value, kindName(value))
		return ""
	}

	return value.Value
}

func (p *parser) list(key, value *yaml.Node) []string {
	if value.Kind != yaml.SequenceNode {
		p.add(value, "%s: expected list of strings, got %s", key.Value, kindName(value))
		return nil
	}

	items := make([]string, 0, len(value.Content))
	for _, item := range value.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			p.add(item, "%s: expected list of strings, got %s", key.Value, kindName(item))
			continue
		}
		items = append(items, item.Value)
	}

	return items
}

func (p *parser) boolean(key, value *yaml.Node) (bool, bool) {
	if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
		p.add(value, "%s: expected bool, got %s", key.Value, kindName(value))
		return false, false
	}

	b, err := strconv.ParseBool(strings.ToLower(value.Value))
	if err != nil {
		p.add(value, "%s: invalid bool %q", key.Value, value.Value)
		return false, false
	}

	return b, true
}

func validMode(mode string) bool {
	return mode == "copy" || mode == "subtree"
}

func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			return "string"
		case "!!bool":
			return "bool"
		case "!!int":
			return "integer"
		case "!!float":
			return "float"
		case "!!null":
			return "null"
		}
		return strings.TrimPrefix(n.Tag, "!!")
	case yaml.SequenceNode:
		return "list"
	case yaml.MappingNode:
		return "mapping"
	case yaml.AliasNode:
		return "alias"
	default:
		return "node"
	}
}

type problemsError []Problem

func (e problemsError) Error() string {
	parts := make([]string, 0, len(e))
	for _, p := range e {
		parts = append(parts, p.String())
	}

	return strings.Join(parts, "; ")
}
