package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadDir scans dir for `*.md` files at the top level (no recursion) and
// parses each as an agent profile. Results are returned in lexicographic
// order of filename for stable output.
//
// Errors:
//   - dir does not exist or is not readable → returns the OS error verbatim
//   - any profile fails to parse → returns the first failure wrapped with
//     its path; profiles parsed so far are NOT returned, since a partial
//     list would mask the failure from callers that just want everything
//   - two profiles within dir declare the same `name:` → returns a
//     duplicate-name error referencing both source paths
//
// Subdirectories are silently ignored. Non-`.md` files (README, license,
// hidden dotfiles, etc.) are silently skipped.
func LoadDir(dir string) ([]*Profile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	profiles := make([]*Profile, 0, len(names))
	seen := map[string]string{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		p, err := loadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if prev, ok := seen[p.Name]; ok {
			return nil, fmt.Errorf("duplicate agent name %q: %s already defined by %s", p.Name, path, prev)
		}
		seen[p.Name] = path
		profiles = append(profiles, p)
	}
	return profiles, nil
}

func loadFile(path string) (*Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p, err := ParseProfile(f)
	if err != nil {
		return nil, err
	}
	p.SourcePath = path
	return p, nil
}
