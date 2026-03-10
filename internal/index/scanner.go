package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

		err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			if d.IsDir() {
				if path != dir && isIgnoredWorkspaceDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(d.Name()) != ".thrift" {
				return nil
			}
			if len(out) >= maxFiles {
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
			out = append(out, scannedFile{
				Path:       path,
				DisplayURI: displayURI,
				Key:        key,
				Size:       info.Size(),
				ModTime:    info.ModTime(),
			})
			return nil
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
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		abs = filepath.Clean(abs)
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
