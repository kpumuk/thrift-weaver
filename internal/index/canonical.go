package index

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
)

type canonicalizeOptions struct {
	goos            string
	cwd             string
	caseInsensitive bool
}

// CanonicalizeDocumentURI canonicalizes a file URI or filesystem path into a display URI and key.
func CanonicalizeDocumentURI(raw string) (string, DocumentKey, error) {
	return canonicalizeDocumentURIWithOptions(raw, runtimeCanonicalizeOptions())
}

func runtimeCanonicalizeOptions() canonicalizeOptions {
	return canonicalizeOptions{
		goos:            runtime.GOOS,
		caseInsensitive: filesystemCaseInsensitive(runtime.GOOS),
	}.withDefaults()
}

func canonicalizeDocumentURIWithOptions(raw string, opts canonicalizeOptions) (string, DocumentKey, error) {
	opts = opts.withDefaults()
	path, err := normalizeInputPath(raw, opts)
	if err != nil {
		return "", "", err
	}
	display := fileURIFromPathForOS(path, opts.goos)
	key := documentKeyForDisplayURIWithCase(display, opts.caseInsensitive)
	return display, key, nil
}

func normalizeInputPath(raw string, opts canonicalizeOptions) (string, error) {
	opts = opts.withDefaults()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty document URI")
	}

	inputPath, err := inputPathFromRaw(raw, opts.goos)
	if err != nil {
		return "", err
	}
	if opts.goos == runtime.GOOS {
		return normalizeRuntimePath(inputPath)
	}
	return normalizePortablePath(inputPath, opts)
}

func (opts canonicalizeOptions) withDefaults() canonicalizeOptions {
	if opts.goos == "" {
		opts.goos = runtime.GOOS
	}
	if opts.cwd == "" {
		opts.cwd, _ = os.Getwd()
	}
	return opts
}

func inputPathFromRaw(raw, goos string) (string, error) {
	if looksLikeWindowsAbsolutePath(raw) {
		return raw, nil
	}

	u, err := url.Parse(raw)
	if err == nil && u.Scheme != "" {
		if u.Scheme != "file" {
			return "", fmt.Errorf("unsupported URI scheme %q", u.Scheme)
		}
		return fileURIPathToOSPath(u, goos), nil
	}
	return raw, nil
}

func normalizeRuntimePath(inputPath string) (string, error) {
	abs, err := filepath.Abs(inputPath)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	//nolint:gosec // Canonicalization intentionally probes arbitrary candidate paths to resolve existing symlinks.
	if info, statErr := os.Stat(abs); statErr == nil && info != nil {
		resolved, evalErr := filepath.EvalSymlinks(abs)
		if evalErr == nil {
			abs = filepath.Clean(resolved)
		}
	}
	return abs, nil
}

func normalizePortablePath(inputPath string, opts canonicalizeOptions) (string, error) {
	switch opts.goos {
	case "windows":
		return normalizePortableWindowsPath(inputPath, opts.cwd)
	default:
		return normalizePortablePOSIXPath(inputPath, opts.cwd), nil
	}
}

func normalizePortableWindowsPath(inputPath, cwd string) (string, error) {
	slash := strings.ReplaceAll(strings.TrimSpace(inputPath), `\`, "/")
	switch {
	case strings.HasPrefix(slash, "/") && len(slash) >= 3 && isASCIILetter(slash[1]) && slash[2] == ':':
		slash = slash[1:]
	case looksLikeWindowsAbsolutePath(slash), strings.HasPrefix(slash, "//"):
	case cwd != "":
		slash = pathpkg.Join(strings.ReplaceAll(cwd, `\`, "/"), slash)
	default:
		return "", fmt.Errorf("relative windows path %q requires cwd", inputPath)
	}

	slash = pathpkg.Clean(slash)
	if looksLikeWindowsAbsolutePath(slash) {
		slash = strings.ToUpper(slash[:1]) + slash[1:]
	}
	return strings.ReplaceAll(slash, "/", `\`), nil
}

func normalizePortablePOSIXPath(inputPath, cwd string) string {
	slash := strings.ReplaceAll(strings.TrimSpace(inputPath), `\`, "/")
	if !strings.HasPrefix(slash, "/") {
		slash = pathpkg.Join(filepath.ToSlash(cwd), slash)
	}
	return filepath.Clean(filepath.FromSlash(pathpkg.Clean(slash)))
}

func fileURIPathToOSPath(u *url.URL, goos string) string {
	switch goos {
	case "windows":
		if u.Host != "" && u.Host != "localhost" {
			rest := strings.TrimPrefix(u.Path, "/")
			return `\\` + u.Host + `\` + strings.ReplaceAll(rest, "/", `\`)
		}
		p := strings.TrimPrefix(u.Path, "/")
		if looksLikeWindowsAbsolutePath(p) {
			p = strings.ToUpper(p[:1]) + p[1:]
		}
		return strings.ReplaceAll(p, "/", `\`)
	default:
		return filepath.FromSlash(u.Path)
	}
}

func fileURIFromPathForOS(path, goos string) string {
	switch goos {
	case "windows":
		if strings.HasPrefix(path, `\\`) {
			slash := strings.TrimPrefix(strings.ReplaceAll(path, `\`, "/"), "//")
			host, rest, _ := strings.Cut(slash, "/")
			return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String()
		}
		slash := strings.ReplaceAll(path, `\`, "/")
		if looksLikeWindowsAbsolutePath(slash) {
			slash = "/" + strings.ToUpper(slash[:1]) + slash[1:]
		}
		return (&url.URL{Scheme: "file", Path: slash}).String()
	default:
		return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	}
}

func documentKeyForDisplayURIWithCase(display string, caseInsensitive bool) DocumentKey {
	if !caseInsensitive {
		return DocumentKey(display)
	}
	u, err := url.Parse(display)
	if err != nil {
		return DocumentKey(display)
	}
	u.Path = strings.ToLower(u.Path)
	return DocumentKey(u.String())
}

func filesystemCaseInsensitive(goos string) bool {
	switch goos {
	case "windows", "darwin":
		return true
	default:
		return false
	}
}

func filePathFromDocumentURI(display string) (string, error) {
	u, err := url.Parse(display)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme %q", u.Scheme)
	}
	return filepath.Clean(fileURIPathToOSPath(u, runtime.GOOS)), nil
}

func looksLikeWindowsAbsolutePath(in string) bool {
	in = strings.TrimSpace(in)
	return len(in) >= 3 && isASCIILetter(in[0]) && in[1] == ':' && (in[2] == '\\' || in[2] == '/')
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
