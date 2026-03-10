package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type scannedFile struct {
	Path       string
	DisplayURI string
	Key        DocumentKey
	Size       int64
	ModTime    time.Time
}

func scanWorkspace(ctx context.Context, roots []string, includeDirs []string, maxFiles int, maxFileBytes int64) ([]scannedFile, error) {
	dirs, err := scanDirectories(roots, includeDirs)
	if err != nil {
		return nil, err
	}
	allowedRoots, err := canonicalScanRoots(dirs)
	if err != nil {
		return nil, err
	}

	out := make([]scannedFile, 0, 16)
	seen := make(map[DocumentKey]struct{})
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", dir, err)
		}
		if !info.IsDir() {
			continue
		}

		matchersByDir := make(map[string][]*gitIgnoreMatcher)
		rootMatchers, err := gitIgnoreMatchersForDir(dir, nil)
		if err != nil {
			return nil, err
		}
		matchersByDir[dir] = rootMatchers

		err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return walkWorkspaceDir(path, dir, d.Name(), matchersByDir)
			}
			return walkWorkspaceFile(path, d, matchersByDir, allowedRoots, seen, &out, maxFiles, maxFileBytes)
		})
		if err != nil {
			return nil, err
		}
	}

	slices.SortFunc(out, func(a, b scannedFile) int {
		switch {
		case a.Key < b.Key:
			return -1
		case a.Key > b.Key:
			return 1
		default:
			return 0
		}
	})
	return out, nil
}

func scanDirectories(roots []string, includeDirs []string) ([]string, error) {
	out := make([]string, 0, len(roots)+len(includeDirs))
	seen := make(map[string]struct{})

	addDir := func(dir string) error {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return nil
		}
		abs, err := normalizeRuntimePath(dir)
		if err != nil {
			return err
		}
		if _, ok := seen[abs]; ok {
			return nil
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
		return nil
	}

	for _, root := range roots {
		if err := addDir(root); err != nil {
			return nil, err
		}
	}
	for _, inc := range includeDirs {
		if filepath.IsAbs(inc) {
			if err := addDir(inc); err != nil {
				return nil, err
			}
			continue
		}
		for _, root := range roots {
			if err := addDir(filepath.Join(root, inc)); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func isIgnoredWorkspaceDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".idea", ".vscode":
		return true
	default:
		return false
	}
}

func canonicalScanRoots(dirs []string) ([]string, error) {
	out := make([]string, 0, len(dirs))
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		abs, err := normalizeRuntimePath(dir)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out, nil
}

func pathWithinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithinRoot(path, root) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func walkWorkspaceDir(path string, root string, name string, matchersByDir map[string][]*gitIgnoreMatcher) error {
	if path != root && isIgnoredWorkspaceDir(name) {
		return filepath.SkipDir
	}
	if path == root {
		return nil
	}

	matchers := matchersByDir[filepath.Dir(path)]
	ignored, err := matchesGitIgnore(matchers, path, true)
	if err != nil {
		return err
	}
	if ignored {
		return filepath.SkipDir
	}

	matchersByDir[path], err = gitIgnoreMatchersForDir(path, matchers)
	return err
}

func walkWorkspaceFile(path string, d os.DirEntry, matchersByDir map[string][]*gitIgnoreMatcher, allowedRoots []string, seen map[DocumentKey]struct{}, out *[]scannedFile, maxFiles int, maxFileBytes int64) error {
	ignored, err := matchesGitIgnore(matchersByDir[filepath.Dir(path)], path, false)
	if err != nil {
		return err
	}
	if ignored || filepath.Ext(d.Name()) != ".thrift" {
		return nil
	}
	if len(*out) >= maxFiles {
		return fmt.Errorf("workspace file limit exceeded: maxFiles=%d", maxFiles)
	}

	info, err := d.Info()
	if err != nil {
		return err
	}
	if maxFileBytes > 0 && info.Size() > maxFileBytes {
		return fmt.Errorf("workspace file exceeds size limit: %s (%d > %d)", path, info.Size(), maxFileBytes)
	}

	displayURI, key, err := CanonicalizeDocumentURI(path)
	if err != nil {
		return err
	}
	resolvedPath, err := filePathFromDocumentURI(displayURI)
	if err != nil {
		return err
	}
	if !pathWithinAnyRoot(resolvedPath, allowedRoots) {
		return fmt.Errorf("workspace file resolves outside allowed roots: %s", path)
	}
	if _, ok := seen[key]; ok {
		return nil
	}

	seen[key] = struct{}{}
	*out = append(*out, scannedFile{
		Path:       path,
		DisplayURI: displayURI,
		Key:        key,
		Size:       info.Size(),
		ModTime:    info.ModTime(),
	})
	return nil
}

type gitIgnoreMatcher struct {
	baseDir string
	rules   []gitIgnoreRule
}

type gitIgnoreRule struct {
	anchored bool
	dirOnly  bool
	negated  bool
	hasSlash bool
	regex    *regexp.Regexp
}

func gitIgnoreMatchersForDir(dir string, inherited []*gitIgnoreMatcher) ([]*gitIgnoreMatcher, error) {
	out := slices.Clone(inherited)
	matcher, err := readGitIgnoreMatcher(dir)
	if err != nil {
		return nil, err
	}
	if matcher == nil {
		return out, nil
	}
	return append(out, matcher), nil
}

func readGitIgnoreMatcher(dir string) (*gitIgnoreMatcher, error) {
	path := filepath.Join(dir, ".gitignore")
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	lines := strings.Split(string(src), "\n")
	rules := make([]gitIgnoreRule, 0, len(lines))
	for _, raw := range lines {
		rule, ok, err := parseGitIgnoreRule(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if ok {
			rules = append(rules, rule)
		}
	}
	if len(rules) == 0 {
		return nil, nil
	}
	return &gitIgnoreMatcher{baseDir: dir, rules: rules}, nil
}

func parseGitIgnoreRule(raw string) (gitIgnoreRule, bool, error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return gitIgnoreRule{}, false, nil
	}

	negated := strings.HasPrefix(line, "!")
	if negated {
		line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
	}
	if line == "" {
		return gitIgnoreRule{}, false, nil
	}

	anchored := strings.HasPrefix(line, "/")
	line = strings.TrimPrefix(line, "/")
	dirOnly := strings.HasSuffix(line, "/")
	line = strings.TrimSuffix(line, "/")
	if line == "" {
		return gitIgnoreRule{}, false, nil
	}

	regex, err := regexp.Compile(globPatternToRegex(line))
	if err != nil {
		return gitIgnoreRule{}, false, err
	}
	return gitIgnoreRule{
		anchored: anchored,
		dirOnly:  dirOnly,
		negated:  negated,
		hasSlash: strings.Contains(line, "/"),
		regex:    regex,
	}, true, nil
}

func matchesGitIgnore(matchers []*gitIgnoreMatcher, candidate string, isDir bool) (bool, error) {
	ignored := false
	matched := false
	for _, matcher := range matchers {
		rel, err := filepath.Rel(matcher.baseDir, candidate)
		if err != nil {
			return false, err
		}
		if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

		rel = filepath.ToSlash(rel)
		for _, rule := range matcher.rules {
			if !rule.matches(rel, isDir) {
				continue
			}
			matched = true
			ignored = !rule.negated
		}
	}
	if !matched {
		return false, nil
	}
	return ignored, nil
}

func (r gitIgnoreRule) matches(rel string, isDir bool) bool {
	if rel == "" || (r.dirOnly && !isDir) || r.regex == nil {
		return false
	}
	if r.hasSlash {
		return r.regex.MatchString(rel)
	}

	if r.anchored {
		segment, _, found := strings.Cut(rel, "/")
		if !found {
			segment = rel
		}
		return r.regex.MatchString(segment)
	}

	return slices.ContainsFunc(strings.Split(rel, "/"), func(segment string) bool {
		return r.regex.MatchString(segment)
	})
}

func globPatternToRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch ch := pattern[i]; ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case '/':
			b.WriteByte('/')
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return b.String()
}
