// Package config reads host definitions from OpenSSH client config files.
//
// The parser follows OpenSSH semantics rather than a simplified approximation:
// parameters are resolved first-value-wins across matching Host blocks, file
// scope directives act as defaults for every host, Include is honoured the way
// ssh honours it — read where the line is, and only where the block around it
// matches the host being resolved — and only concrete (non-wildcard) aliases are
// surfaced as connectable hosts.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SSHHost is a single connectable host with every matching directive resolved.
type SSHHost struct {
	Name          string   // the alias used to select this host
	Hostname      string   // HostName, or Name when the block omits it
	User          string   // User, empty means "fall back to $USER"
	Port          string   // Port, empty means 22
	Identity      string   // IdentityFile
	IdentityAgent string   // IdentityAgent
	ProxyJump     string   // ProxyJump
	ProxyCommand  string   // ProxyCommand
	Tags          []string // from a "# Tags: a, b" comment above the Host line
	SourceFile    string   // config file this alias came from
	LineNumber    int      // 1-based line of the Host directive
	IsWildcard    bool     // block patterns contained glob characters
}

// Addr returns the host:port dial target.
func (h SSHHost) Addr() string {
	host := h.Hostname
	if host == "" {
		host = h.Name
	}
	return JoinHostPort(host, h.Port)
}

// DisplayUser returns the user that will be used for authentication.
func (h SSHHost) DisplayUser() string {
	if h.User != "" {
		return h.User
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

// String renders a compact "user@host:port" description for listings.
func (h SSHHost) String() string {
	host := h.Hostname
	if host == "" {
		host = h.Name
	}
	return fmt.Sprintf("%s@%s:%s", h.DisplayUser(), host, portOr(h.Port))
}

func portOr(p string) string {
	if p == "" {
		return "22"
	}
	return p
}

// directive is one "Key value" pair, kept in file order.
type directive struct {
	key   string
	value string
}

// block is a Host/Match section: the patterns that select it plus its directives.
type block struct {
	patterns   []string // may include negations prefixed with '!'
	directives []directive
	file       string
	line       int
	tags       []string
	isMatch    bool // Match blocks are recorded but not applied

	// enclosing holds the patterns of each Host block an Include that produced
	// this one appeared inside, outermost first. It is empty for the blocks a
	// file declares directly. An Include inside a Host block is conditional: ssh
	// reads the named file for the host it was asked for and passes over it when
	// that block does not match, so what the file holds is in play only where
	// every enclosing block covers the host too.
	enclosing [][]string
}

// DefaultSSHConfigPath returns ~/.ssh/config.
func DefaultSSHConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// ParseSSHConfig reads the default config path and returns resolved hosts.
func ParseSSHConfig() ([]SSHHost, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return nil, err
	}
	return ParseSSHConfigFile(path)
}

// ParseSSHConfigFile parses path and returns one resolved entry per concrete alias.
// A missing file is not an error; it yields no hosts.
func ParseSSHConfigFile(path string) ([]SSHHost, error) {
	blocks, err := parseBlocks(path, 0, nil)
	if err != nil {
		return nil, err
	}
	return resolveBlocks(blocks), nil
}

// ParseSSHConfigFiles parses paths in order into one resolved list, as if each
// later file were Included at the end of the first.
//
// Reading them together rather than separately is what makes the arrangement
// worth having: a file-scope directive in the first file — a default User, a
// default IdentityFile — applies to hosts declared in the second, exactly as it
// would to a host in an included file. And because the first obtained value
// wins across the lot, a host defined in both files resolves to the one in the
// first, so a file ohmyssh writes can never shadow the user's own config.
func ParseSSHConfigFiles(paths ...string) ([]SSHHost, error) {
	blocks, err := parseFiles(paths...)
	if err != nil {
		return nil, err
	}
	return resolveBlocks(blocks), nil
}

// parseFiles reads paths as one configuration, in the order given.
//
// Each file is read on its own terms — nothing Includes the second one, however
// it is reached — so none of them carries an enclosing block.
func parseFiles(paths ...string) ([]block, error) {
	var blocks []block
	for _, path := range paths {
		parsed, err := parseBlocks(path, 0, nil)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, parsed...)
	}
	return blocks, nil
}

// ResolveName resolves target the way ssh does when no literal alias names it,
// which is the case a wildcard block covers.
//
// Only a block with something specific in its pattern counts. A bare `Host *`
// is not a definition: its directives are the defaults every host already
// receives, and letting it match would make every mistyped alias resolve to a
// host that does not exist — the mistake Find's suggestions exist to report.
// A pattern that names something — `*.corp.example.com` — defines a machine as
// squarely as an alias does, and refusing it sends the user hunting for a host
// that is right there in the file they are reading.
//
// It reads the files itself because a caller that has resolved the hosts no
// longer holds the blocks, and the names it resolves are not among them: a
// wildcard block is not offered as a browsable host. The cost is one parse on
// the path where nothing matched, which is once per invocation.
func ResolveName(target string, paths ...string) (SSHHost, bool, error) {
	blocks, err := parseFiles(paths...)
	if err != nil {
		return SSHHost{}, false, err
	}
	for _, b := range blocks {
		if b.isMatch || isCatchAll(b.patterns) {
			continue
		}
		if blockMatches(b.patterns, target) && enclosingMatches(b.enclosing, target) {
			return resolveHost(target, blocks), true, nil
		}
	}
	return SSHHost{}, false, nil
}

// resolveBlocks turns parsed blocks into one resolved host per concrete alias,
// in the order the aliases first appear.
func resolveBlocks(blocks []block) []SSHHost {
	// Every concrete alias named by a Host pattern becomes a candidate, in order.
	var names []string
	seen := make(map[string]struct{})
	for _, b := range blocks {
		if b.isMatch {
			continue
		}
		for _, pat := range b.patterns {
			if strings.HasPrefix(pat, "!") || hasGlob(pat) {
				continue
			}
			// A name an Include defines exists only where that Include applies,
			// so it is offered only when the blocks the Include sat inside cover
			// it: ssh would never read the file that names it for that host.
			if !enclosingMatches(b.enclosing, pat) {
				continue
			}
			if _, dup := seen[pat]; dup {
				continue
			}
			seen[pat] = struct{}{}
			names = append(names, pat)
		}
	}

	hosts := make([]SSHHost, 0, len(names))
	for _, name := range names {
		hosts = append(hosts, resolveHost(name, blocks))
	}
	return hosts
}

// resolveHost walks blocks in file order, applying first-value-wins semantics.
func resolveHost(name string, blocks []block) SSHHost {
	host := SSHHost{Name: name}
	set := make(map[string]bool)

	for _, b := range blocks {
		if b.isMatch || !blockMatches(b.patterns, name) || !enclosingMatches(b.enclosing, name) {
			continue
		}
		if len(host.Tags) == 0 && len(b.tags) > 0 {
			host.Tags = append([]string(nil), b.tags...)
		}
		if host.LineNumber == 0 && !isCatchAll(b.patterns) {
			host.LineNumber = b.line
			host.SourceFile = b.file
		}
		for _, d := range b.directives {
			if set[d.key] {
				continue // first obtained value wins, as in OpenSSH
			}
			set[d.key] = true
			switch d.key {
			case "hostname":
				host.Hostname = d.value
			case "user":
				host.User = d.value
			case "port":
				host.Port = d.value
			case "identityfile":
				host.Identity = d.value
			case "identityagent":
				host.IdentityAgent = d.value
			case "proxyjump":
				host.ProxyJump = d.value
			case "proxycommand":
				host.ProxyCommand = d.value
			}
		}
	}

	if host.Hostname == "" {
		host.Hostname = name
	}
	return host
}

// blockMatches reports whether name satisfies the block's pattern list.
// A negation match vetoes the block regardless of positive patterns.
func blockMatches(patterns []string, name string) bool {
	matched := false
	for _, pat := range patterns {
		if neg, ok := strings.CutPrefix(pat, "!"); ok {
			if matchPattern(neg, name) {
				return false
			}
			continue
		}
		if matchPattern(pat, name) {
			matched = true
		}
	}
	return matched
}

// enclosingMatches reports whether every block an Include was read inside covers
// name. A block a file declares directly has nothing enclosing it, and passes.
//
// The patterns are checked together rather than folded into one list because
// they come from different files: `Host *.corp.example.com` in the config and
// `Host *` in the file it includes both have to hold, and no single pattern list
// says that.
func enclosingMatches(enclosing [][]string, name string) bool {
	for _, patterns := range enclosing {
		if !blockMatches(patterns, name) {
			return false
		}
	}
	return true
}

func isCatchAll(patterns []string) bool {
	return slices.Contains(patterns, "*")
}

func hasGlob(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// matchPattern implements OpenSSH's glob: '*' spans any run, '?' a single byte.
func matchPattern(pattern, name string) bool {
	// Iterative backtracking avoids exponential recursion on adversarial input.
	var (
		p, n       int
		star       = -1
		match      int
		patternLen = len(pattern)
		nameLen    = len(name)
	)
	for n < nameLen {
		switch {
		case p < patternLen && (pattern[p] == '?' || pattern[p] == name[n]):
			p++
			n++
		case p < patternLen && pattern[p] == '*':
			star = p
			match = n
			p++
		case star != -1:
			p = star + 1
			match++
			n = match
		default:
			return false
		}
	}
	for p < patternLen && pattern[p] == '*' {
		p++
	}
	return p == patternLen
}

// parseBlocks reads path into ordered blocks, recursing into Include globs.
// depth bounds recursion so a self-including config cannot loop forever, and
// enclosing carries the patterns of every Host block this file is being read
// inside, for the blocks it hands back to record.
func parseBlocks(path string, depth int, enclosing [][]string) ([]block, error) {
	const maxIncludeDepth = 16
	if depth > maxIncludeDepth {
		return nil, fmt.Errorf("include depth exceeded at %s", path)
	}

	path = ExpandPath(path)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// The file is opened read-only, so a failure to close it carries no
	// information worth propagating.
	defer func() { _ = f.Close() }()

	var (
		blocks      []block
		global      = block{file: path, enclosing: enclosing}
		pendingTags []string
		current     *block
	)

	scanner := bufio.NewScanner(f)
	// Config lines are short; a larger buffer only guards against pathological input.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()

		if tags, ok := parseTagComment(raw); ok {
			pendingTags = tags
			continue
		}
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}

		key, value := splitDirective(line)
		if key == "" {
			continue
		}

		switch key {
		case "include":
			// The included blocks belong where the Include line is: ssh reads the
			// file at that point, and from there on the values it sets compete for
			// first-value-wins like any other. The open block is therefore closed
			// here, ahead of them, and a fresh one carrying the same Host line
			// takes its place afterwards — the two halves resolve as one block,
			// with the included directives between them in file order.
			//
			// Left alone, the enclosing block would only be appended at the next
			// Host line, which puts it after everything it Included. First-value-
			// wins would then let an included `Host *` — the shape a conf.d
			// defaults file usually takes — override the very block that included
			// it, and the user's own User, Port and ProxyJump for that host would
			// be dropped without a word.
			//
			// A block that has not recorded anything yet needs no half: it is
			// appended after the included blocks either way, which is already the
			// order the file is in.
			//
			// Which hosts the file speaks for is a second question, and the answer
			// ssh gives is narrower than where the Include line sits: an Include
			// inside a Host block is conditional, read for the host being resolved
			// and passed over when that block does not cover it. The enclosing
			// patterns therefore travel down with everything the file contributes —
			// its leading directives included, since ssh reads those in the
			// enclosing block's scope too — and resolution leaves out what they do
			// not cover.
			//
			// A Match block is left out of this: its condition is runtime state
			// this parser does not evaluate, so an Include inside one is taken as
			// written rather than guessed at.
			if current != nil && (len(current.directives) > 0 || len(current.tags) > 0) {
				blocks = append(blocks, *current)
				continuation := *current
				continuation.directives = nil
				// The tags belong to the Host line, which stays on the first half.
				continuation.tags = nil
				current = &continuation
			}
			inner := enclosing
			if current != nil && !current.isMatch {
				inner = append(slices.Clone(enclosing), current.patterns)
			}
			for _, pattern := range splitFields(value) {
				pattern = ExpandPath(pattern)
				matches, err := filepath.Glob(pattern)
				if err != nil {
					continue
				}
				for _, m := range matches {
					included, err := parseBlocks(m, depth+1, inner)
					if err != nil {
						return nil, err
					}
					blocks = append(blocks, included...)
				}
			}
		case "host":
			if current != nil {
				blocks = append(blocks, *current)
			}
			current = &block{
				patterns:  splitFields(value),
				file:      path,
				line:      lineNo,
				tags:      pendingTags,
				enclosing: enclosing,
			}
			pendingTags = nil
		case "match":
			// Match blocks are conditional on runtime state we do not model;
			// record them so their directives never leak into other hosts.
			if current != nil {
				blocks = append(blocks, *current)
			}
			current = &block{isMatch: true, file: path, line: lineNo, enclosing: enclosing}
			pendingTags = nil
		default:
			d := directive{key: key, value: value}
			if current == nil {
				global.directives = append(global.directives, d)
			} else {
				current.directives = append(current.directives, d)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if current != nil {
		blocks = append(blocks, *current)
	}

	// File-scope directives apply to every host, so they lead the block list.
	if len(global.directives) > 0 {
		global.patterns = []string{"*"}
		blocks = append([]block{global}, blocks...)
	}
	return blocks, nil
}

// parseTagComment recognises the "# Tags: a, b" convention used to label hosts.
func parseTagComment(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "#") {
		return nil, false
	}
	rest := strings.TrimSpace(trimmed[1:])
	lowerRest := strings.ToLower(rest)
	if !strings.HasPrefix(lowerRest, "tag:") && !strings.HasPrefix(lowerRest, "tags:") {
		return nil, false
	}
	_, after, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, false
	}
	var tags []string
	for tag := range strings.SplitSeq(after, ",") {
		tag = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tag), "#"))
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 {
		return nil, false
	}
	return tags, true
}

// stripComment drops a '#' comment. A '#' only starts a comment at the start of
// a token, so values like "ProxyCommand foo#bar" survive intact.
func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

// splitDirective parses "Key value", "Key=value" and "Key = value" forms.
func splitDirective(line string) (key, value string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.IndexAny(trimmed, " \t=")
	if idx == -1 {
		return strings.ToLower(trimmed), ""
	}
	key = strings.ToLower(strings.TrimSpace(trimmed[:idx]))
	value = strings.TrimSpace(trimmed[idx:])
	value = strings.TrimSpace(strings.TrimPrefix(value, "="))
	value = strings.TrimSpace(unquote(value))
	return key, value
}

// splitFields splits on whitespace, honouring double quotes.
func splitFields(s string) []string {
	var (
		fields  []string
		current strings.Builder
		quoted  bool
	)
	flush := func() {
		if current.Len() > 0 {
			fields = append(fields, current.String())
			current.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case (r == ' ' || r == '\t') && !quoted:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return fields
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// ExpandPath resolves a leading "~" and makes the path absolute. Relative paths
// are resolved against ~/.ssh, matching how OpenSSH treats IdentityFile and
// Include values.
func ExpandPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	} else if strings.HasPrefix(path, "~") {
		// ~user is not supported; leave as-is rather than guessing.
		return path
	} else if !filepath.IsAbs(path) {
		// Relative Include paths resolve against ~/.ssh, as OpenSSH does.
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".ssh", path)
		}
	}
	return filepath.Clean(path)
}

// JoinHostPort builds the host:port form an address is dialled and looked up in
// known_hosts under. An empty port means 22, as it does everywhere else a host
// is described.
//
// net.JoinHostPort is not used because it would bracket a literal that already
// carries brackets — ssh config allows both spellings of an IPv6 address, and
// both have to reach the same place.
func JoinHostPort(host, port string) string {
	if port == "" {
		port = "22"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}
