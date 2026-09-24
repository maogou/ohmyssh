package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/maogou/ohmyssh/internal/pkg/appdir"
)

// hostsFileName is what ohmyssh's own host file is called, inside the directory
// appdir names.
const hostsFileName = "hosts"

// DefaultHostsPath is the file hosts added through ohmyssh are written to.
func DefaultHostsPath() (string, error) {
	return appdir.Path(hostsFileName)
}

// NewHost is a host the user typed into the browser's add form: the fields of a
// Host block, before any of them has been written down.
//
// Only the alias is required. A field left empty is left out of the block, which
// is how a Host block is read anyway — an omitted HostName means the alias is
// the address, an omitted User means the login of whoever is running this, an
// omitted Port means 22.
type NewHost struct {
	Alias string
	Host  string
	User  string
	Port  string
	Tags  []string
}

// Validate reports whether these fields can be written down as a Host block and
// read back as the host the user meant.
//
// The line-break check is the one that matters: a value carrying a break does
// not stay a value, it starts a directive of its own on the next line, and the
// file is read back as configuration. The rest is about what an alias may be,
// since a glob or a negation there would change how *other* blocks match rather
// than merely failing to match this one.
func (h NewHost) Validate() error {
	if err := usableField("alias", h.Alias); err != nil {
		return err
	}
	if h.Alias == "" {
		return errors.New("alias cannot be empty")
	}
	if strings.ContainsAny(h.Alias, "*?!#") {
		return fmt.Errorf("alias %q cannot contain * ? ! or #", h.Alias)
	}

	for _, field := range []struct{ name, value string }{
		{"host", h.Host},
		{"user", h.User},
		{"port", h.Port},
	} {
		if err := usableField(field.name, field.value); err != nil {
			return err
		}
	}
	if h.Port != "" {
		port, err := strconv.Atoi(h.Port)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("port %q is not a port number", h.Port)
		}
	}

	for _, tag := range h.Tags {
		if err := usableField("tag", tag); err != nil {
			return err
		}
		if strings.Contains(tag, ",") {
			return fmt.Errorf("tag %q cannot contain a comma", tag)
		}
	}
	return nil
}

// usableField rejects a value that would not survive being written on the one
// line it is meant to occupy.
func usableField(name, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s cannot contain a line break", name)
	}
	if strings.ContainsAny(value, " \t") {
		return fmt.Errorf("%s cannot contain a space", name)
	}
	return nil
}

// Block renders the host the way it is written to a config file, in the format
// ssh's own files use down to the four-space indent, so that the file ohmyssh
// keeps can be read, edited and version controlled like any other.
func (h NewHost) Block() string {
	var b strings.Builder
	if len(h.Tags) > 0 {
		// Above the block it labels, which is where the parser looks for it.
		fmt.Fprintf(&b, "# Tags: %s\n", strings.Join(h.Tags, ", "))
	}
	fmt.Fprintf(&b, "Host %s\n", h.Alias)
	writeDirective(&b, "HostName", h.Host)
	writeDirective(&b, "User", h.User)
	writeDirective(&b, "Port", h.Port)
	return b.String()
}

// writeDirective writes one "Key value" line, or nothing at all when there is no
// value to write: a bare "User" is not the same as no User at all.
func writeDirective(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "    %s %s\n", key, value)
}

// AddHost appends h to the config file at path, creating the file and the
// directory above it when they are missing. Nothing already in the file is
// touched: what is added sits alongside it.
func AddHost(path string, h NewHost) error {
	if err := h.Validate(); err != nil {
		return err
	}

	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return writeHost(path, appendHost(body, h))
}

// appendHost is the file's new contents: what was already there, made to end on
// a blank line if it did not, and then the block.
func appendHost(body []byte, h NewHost) string {
	var b strings.Builder
	b.Write(body)

	if existing := string(body); len(body) > 0 {
		if !strings.HasSuffix(existing, "\n") {
			b.WriteString("\n")
		}
		// A blank line between the new block and what came before, unless the file
		// already ends on one.
		if !strings.HasSuffix(existing, "\n\n") {
			b.WriteString("\n")
		}
	}

	b.WriteString(h.Block())
	return b.String()
}

// RemoveHost deletes the blocks naming alias from the config file at path.
//
// The file is edited by line rather than written back from what was parsed out of
// it, so everything the user put there by hand — comments, spacing, the order of
// the directives — is still there afterwards. A block takes its "# Tags:" comment
// with it: left behind, that comment would label whichever block follows, which
// is a host nobody tagged. A path that cannot be read, or that holds no block
// naming alias, is an error rather than a quiet success.
func RemoveHost(path, alias string) error {
	if alias == "" {
		return errors.New("alias cannot be empty")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	edited, removed, err := withoutHost(string(body), alias)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !removed {
		return fmt.Errorf("no host named %q in %s", alias, path)
	}
	return writeHost(path, edited)
}

// withoutHost returns body with every block naming alias taken out, and whether
// there was one. Every one of them goes: a file with two blocks for the same
// alias would keep resolving to the second after only the first was removed,
// which is a delete that leaves the host on screen.
func withoutHost(body, alias string) (string, bool, error) {
	lines := strings.Split(body, "\n")
	// A file ending in a newline splits off a final empty element, which is that
	// newline rather than a line of its own. It is kept out of every range below:
	// removing it would take the file's last line ending with it.
	end := len(lines)
	if end > 0 && lines[end-1] == "" {
		end--
	}

	var (
		kept    = make([]string, 0, len(lines))
		removed bool
	)
	for i := 0; i < end; i++ {
		patterns, isHostLine := hostPatterns(lines[i])
		if !isHostLine || !slices.Contains(patterns, alias) {
			kept = append(kept, lines[i])
			continue
		}
		removed = true

		if rest := removePattern(patterns, alias); len(rest) > 0 {
			// The line names other aliases too, so it stays and loses one name: the
			// block belongs to hosts that are still there.
			line, err := hostLine(lines[i], rest)
			if err != nil {
				return "", false, err
			}
			kept = append(kept, line)
			continue
		}

		// The block's own lines end at its last directive. The lines below that —
		// blank, or comments — are left where they are: a comment at the foot of a
		// block reads as a heading for the block under it, and the next block's own
		// "# Tags:" line is one of them, so taking them would tag a host nobody
		// tagged.
		stop := blockStop(lines, i, blockEnd(lines, i, end))

		// Its preamble goes with it: the "# Tags:" comment above it, which would
		// otherwise label whichever block follows, and the blank line that separated
		// it from the block above.
		kept = dropPreamble(kept)

		// Where the block sat, one blank line goes back — the blocks left behind
		// still need separating from each other, and the blank lines that did it are
		// the ones the block just took with it. A file with nothing on one side of
		// the hole gets nothing: one that began on a blank line would read as a
		// mistake, and one that ends on a blank line is a trailing blank line the
		// user did not write.
		j := stop + 1
		for j < end && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j < end && len(kept) > 0 {
			kept = append(kept, "")
		}
		i = j - 1
	}
	kept = append(kept, lines[end:]...)

	return strings.Join(kept, "\n"), removed, nil
}

// hostPatterns reads a line as a Host directive and returns the patterns it
// names, if it is one.
func hostPatterns(line string) ([]string, bool) {
	key, value := splitDirective(stripComment(line))
	if key != "host" {
		return nil, false
	}
	return splitFields(value), true
}

// removePattern returns patterns without the alias, every mention of it, so that
// a hand-written line naming the same alias twice does not keep the host alive
// on the strength of the second one.
func removePattern(patterns []string, alias string) []string {
	rest := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == alias {
			continue
		}
		rest = append(rest, pattern)
	}
	return rest
}

// blockEnd is where the block starting at line from stops being the last one: at
// the next Host or Match directive, which begins the block after it, or at the
// end of the file.
func blockEnd(lines []string, from, end int) int {
	for i := from + 1; i < end; i++ {
		if key, _ := splitDirective(stripComment(lines[i])); key == "host" || key == "match" {
			return i
		}
	}
	return end
}

// blockStop is the last line of the block starting at from: its final directive,
// with the blank lines and comments that trail it left out. A block of nothing
// but its Host line stops where it starts.
func blockStop(lines []string, from, end int) int {
	stop := from
	for i := from; i < end; i++ {
		if trimmed := strings.TrimSpace(lines[i]); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		stop = i
	}
	return stop
}

// dropPreamble takes off the end of kept the lines that belonged to the block
// just removed: its "# Tags:" comment, and the blank lines that separated it
// from the block above. Another block's comments — the parser ignores them — are
// left alone, since only a "# Tags:" line is known to change what the file means.
func dropPreamble(kept []string) []string {
	for len(kept) > 0 {
		last := kept[len(kept)-1]
		if _, isTag := parseTagComment(last); isTag {
			kept = kept[:len(kept)-1]
			continue
		}
		if strings.TrimSpace(last) == "" {
			kept = kept[:len(kept)-1]
			continue
		}
		break
	}
	return kept
}

// hostLine rewrites a Host directive without one of its patterns, keeping the
// indentation it had.
//
// A line carrying a quote or a comment is refused rather than rewritten: putting
// it back together drops both, and a quoted alias is exactly the one likely to
// hold something that has to stay quoted. Only a host file edited by hand can
// have such a line — the one ohmyssh writes names a single alias — so the user is
// the one who can say what it meant.
func hostLine(line string, patterns []string) (string, error) {
	if strings.ContainsAny(line, `"#`) {
		return "", errors.New("a Host line naming other aliases is quoted or commented by hand; edit the file to remove the host")
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	return indent + "Host " + strings.Join(patterns, " "), nil
}

// writeHost replaces path with content.
//
// The write lands in a temporary file beside it and is renamed into place, so
// that a failure partway through cannot leave half a Host block behind: a
// truncated block does not merely fail to add a host, it changes how everything
// after it in the file parses. The path is resolved through any symlink first,
// because the rename is what would replace one — a hosts file kept in a dotfiles
// repository and linked into place has to stay a link.
func writeHost(path, content string) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("resolve %s: %w", path, err)
		}
		// Nothing there yet, which is the ordinary case for a first host.
		target = path
		dir := filepath.Dir(target)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	// CreateTemp writes the file 0600, which is the mode its contents want.
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*")
	if err != nil {
		return fmt.Errorf("create a temporary file beside %s: %w", target, err)
	}
	defer func() {
		// Once the rename has happened there is nothing to remove, and before it
		// there is a file here the user never asked for.
		_ = os.Remove(tmp.Name())
	}()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}
