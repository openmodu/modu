package resource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResourceRef points to a file or directory discovered from a package.
type ResourceRef struct {
	Path    string
	Source  string
	Package string
}

// PackageInfo describes one local resource package.
type PackageInfo struct {
	Name    string
	Path    string
	Source  string
	Enabled bool
	Skills  []ResourceRef
	Prompts []ResourceRef
}

// ResourceSnapshot is the unified resource view discovered by Loader.
type ResourceSnapshot struct {
	ContextFiles []ContextFile
	Packages     []PackageInfo
	SkillPaths   []ResourceRef
	PromptPaths  []ResourceRef
}

type packageManifest struct {
	Name    string   `json:"name"`
	Enabled *bool    `json:"enabled,omitempty"`
	Skills  []string `json:"skills,omitempty"`
	Prompts []string `json:"prompts,omitempty"`
}

// LoadResources discovers all resource classes managed by Loader.
func (l *Loader) LoadResources() ResourceSnapshot {
	packages := l.LoadPackages()
	var skillPaths []ResourceRef
	var promptPaths []ResourceRef
	for _, pkg := range packages {
		if !pkg.Enabled {
			continue
		}
		skillPaths = append(skillPaths, pkg.Skills...)
		promptPaths = append(promptPaths, pkg.Prompts...)
	}
	return ResourceSnapshot{
		ContextFiles: l.LoadContextFiles(),
		Packages:     packages,
		SkillPaths:   dedupeResourceRefs(skillPaths),
		PromptPaths:  dedupeResourceRefs(promptPaths),
	}
}

// LoadPackages discovers local resource package manifests from global and
// project package directories. Project packages override global packages with
// the same name.
func (l *Loader) LoadPackages() []PackageInfo {
	byName := make(map[string]PackageInfo)
	for _, root := range []struct {
		path   string
		source string
	}{
		{filepath.Join(l.agentDir, "packages"), "user"},
		{filepath.Join(l.cwd, ".coding_agent", "packages"), "project"},
	} {
		for _, pkg := range loadPackagesFromRoot(root.path, root.source) {
			byName[pkg.Name] = pkg
		}
	}

	packages := make([]PackageInfo, 0, len(byName))
	for _, pkg := range byName {
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Source == packages[j].Source {
			return packages[i].Name < packages[j].Name
		}
		return packages[i].Source < packages[j].Source
	})
	return packages
}

func loadPackagesFromRoot(root, source string) []PackageInfo {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var packages []PackageInfo
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		pkgPath := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(pkgPath, "package.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var manifest packageManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		name := strings.TrimSpace(manifest.Name)
		if name == "" {
			name = entry.Name()
		}
		enabled := true
		if manifest.Enabled != nil {
			enabled = *manifest.Enabled
		}
		packages = append(packages, PackageInfo{
			Name:    name,
			Path:    pkgPath,
			Source:  source,
			Enabled: enabled,
			Skills:  expandResourcePatterns(pkgPath, name, source, manifest.Skills),
			Prompts: expandResourcePatterns(pkgPath, name, source, manifest.Prompts),
		})
	}
	return packages
}

func expandResourcePatterns(baseDir, packageName, source string, patterns []string) []ResourceRef {
	var refs []ResourceRef
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.HasPrefix(pattern, "!") {
			continue
		}
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		if strings.Contains(pattern, "**") {
			for _, match := range expandDoubleStar(pattern) {
				refs = append(refs, ResourceRef{Path: match, Source: source + "/" + packageName, Package: packageName})
			}
			continue
		}
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			if _, statErr := os.Stat(pattern); statErr == nil {
				matches = []string{pattern}
			}
		}
		for _, match := range matches {
			refs = append(refs, ResourceRef{Path: match, Source: source + "/" + packageName, Package: packageName})
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	return dedupeResourceRefs(refs)
}

func expandDoubleStar(pattern string) []string {
	idx := strings.Index(pattern, "**")
	root := strings.TrimSuffix(pattern[:idx], string(filepath.Separator))
	if root == "" {
		root = "."
	}
	tail := strings.TrimPrefix(pattern[idx+2:], string(filepath.Separator))
	var matches []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if tail == "" || strings.HasSuffix(rel, tail) {
			matches = append(matches, path)
		}
		return nil
	})
	sort.Strings(matches)
	return matches
}

func dedupeResourceRefs(refs []ResourceRef) []ResourceRef {
	seen := make(map[string]struct{}, len(refs))
	out := make([]ResourceRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Path == "" {
			continue
		}
		clean := filepath.Clean(ref.Path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		ref.Path = clean
		out = append(out, ref)
	}
	return out
}
