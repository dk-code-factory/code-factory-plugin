package prreview

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type Location struct {
	Path string
	Line int
	Side string
}
type Patch struct {
	Files map[string]bool
	Added map[Location]bool
	Lines map[Location]bool
}

var hunk = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func SafePath(p string) bool {
	return p != "" && p != "." && !strings.ContainsAny(p, "\x00\r\n\\") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}
func patchName(raw string) (string, error) {
	name := strings.SplitN(raw, "\t", 2)[0]
	if strings.HasPrefix(name, `"`) {
		v, err := strconv.Unquote(name)
		if err != nil {
			return "", err
		}
		name = v
	}
	if name == "/dev/null" {
		return "", nil
	}
	if !strings.HasPrefix(name, "a/") && !strings.HasPrefix(name, "b/") {
		return "", errors.New("invalid diff path")
	}
	name = name[2:]
	if !SafePath(name) {
		return "", errors.New("unsafe diff path")
	}
	return name, nil
}

func headerName(raw, prefix string) (string, bool) {
	if strings.HasPrefix(raw, `"`) {
		decoded, err := strconv.Unquote(raw)
		if err != nil {
			return "", false
		}
		raw = decoded
	}
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(raw, prefix)
	return name, SafePath(name)
}

// Git leaves spaces unquoted, including " b/" within either path. Resolve the
// header against its file metadata, or equal paths for mode-only/binary changes.
func diffFileName(header, oldpath, destination string) (string, error) {
	var name string
	matches := 0
	for i := range header {
		if header[i] != ' ' {
			continue
		}
		old, oldOK := headerName(header[:i], "a/")
		new, newOK := headerName(header[i+1:], "b/")
		if !oldOK || !newOK || (oldpath != "" && old != oldpath) || (destination != "" && new != destination) {
			continue
		}
		if old == new {
			return new, nil
		}
		name = new
		matches++
	}
	if matches != 1 {
		return "", errors.New("invalid or ambiguous diff file header")
	}
	return name, nil
}

func ParsePatch(text string) (Patch, error) {
	p := Patch{map[string]bool{}, map[Location]bool{}, map[Location]bool{}}
	var oldpath, current, renamedFrom, renamed, header string
	hasHeader, hasOldPath := false, false
	old, new, oldLeft, newLeft := 0, 0, 0, 0
	inHunk := false
	finish := func() error {
		if inHunk && (oldLeft != 0 || newLeft != 0) {
			return errors.New("incomplete diff hunk")
		}
		source, name := oldpath, current
		if renamedFrom != "" {
			if hasOldPath && source != renamedFrom {
				return errors.New("renamed source and diff source disagree")
			}
			source = renamedFrom
		}
		if renamed != "" {
			if name != "" && name != renamed {
				return errors.New("renamed path and diff destination disagree")
			}
			name = renamed
		}
		if hasHeader {
			var err error
			name, err = diffFileName(header, source, name)
			if err != nil {
				return err
			}
		}
		if name != "" {
			p.Files[name] = true
		}
		return nil
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if err := finish(); err != nil {
				return p, err
			}
			oldpath, current, renamedFrom, renamed = "", "", "", ""
			hasOldPath = false
			inHunk = false
			header, hasHeader = strings.TrimPrefix(line, "diff --git "), true
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			if err := finish(); err != nil {
				return p, err
			}
			m := hunk.FindStringSubmatch(line)
			if m == nil || current == "" {
				return p, errors.New("malformed diff hunk")
			}
			old, _ = strconv.Atoi(m[1])
			new, _ = strconv.Atoi(m[3])
			oldLeft, newLeft = 1, 1
			if m[2] != "" {
				oldLeft, _ = strconv.Atoi(m[2])
			}
			if m[4] != "" {
				newLeft, _ = strconv.Atoi(m[4])
			}
			inHunk = true
			continue
		}
		if inHunk {
			if line == "" || strings.HasPrefix(line, `\ No newline`) {
				continue
			}
			switch line[0] {
			case '+':
				p.Added[Location{current, new, "RIGHT"}] = true
				p.Lines[Location{current, new, "RIGHT"}] = true
				new++
				newLeft--
			case '-':
				p.Lines[Location{current, old, "LEFT"}] = true
				old++
				oldLeft--
			case ' ':
				p.Lines[Location{current, old, "LEFT"}] = true
				p.Lines[Location{current, new, "RIGHT"}] = true
				old++
				new++
				oldLeft--
				newLeft--
			default:
				return p, fmt.Errorf("unexpected diff content %q", line)
			}
			if oldLeft < 0 || newLeft < 0 {
				return p, errors.New("diff hunk exceeds declared range")
			}
			continue
		}
		if strings.HasPrefix(line, "--- ") {
			name, err := patchName(line[4:])
			if err != nil {
				return p, err
			}
			oldpath = name
			hasOldPath = true
		}
		if strings.HasPrefix(line, "+++ ") {
			name, err := patchName(line[4:])
			if err != nil {
				return p, err
			}
			current = name
			if current == "" {
				current = oldpath
			}
		}
		if strings.HasPrefix(line, "rename from ") || strings.HasPrefix(line, "rename to ") {
			kind, name, _ := strings.Cut(strings.TrimPrefix(line, "rename "), " ")
			if strings.HasPrefix(name, `"`) {
				var err error
				name, err = strconv.Unquote(name)
				if err != nil {
					return p, err
				}
			}
			if !SafePath(name) {
				return p, errors.New("unsafe renamed path")
			}
			if kind == "from" {
				if renamedFrom != "" && renamedFrom != name {
					return p, errors.New("conflicting renamed sources")
				}
				renamedFrom = name
			} else {
				if renamed != "" && renamed != name {
					return p, errors.New("conflicting renamed destinations")
				}
				renamed = name
			}
		}
		if strings.HasPrefix(line, "Binary files ") {
			return p, errors.New("binary diff is incomplete; capture with --binary")
		}
	}
	if err := finish(); err != nil {
		return p, err
	}
	return p, nil
}
