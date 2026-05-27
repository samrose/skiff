// Package classify provides a pure-Go tarball classifier.
// It reads an npm tarball stream exactly once and returns the highest-precedence
// Classification from the six possible classes (broken, suspicious,
// has_native_code, fetches_at_install, has_lifecycle_script, pure_js).
//
// Zero non-stdlib dependencies — the classifier is safe to embed in any context.
package classify

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Version is the classifier rule-set version. Bump when any rule changes so
// analytics can group classifications by the rule version that produced them.
const Version = "0.1.0"

// Class is the classification result for a package.
type Class string

const (
	ClassBroken             Class = "broken"
	ClassSuspicious         Class = "suspicious"
	ClassHasNativeCode      Class = "has_native_code"
	ClassFetchesAtInstall   Class = "fetches_at_install"
	ClassHasLifecycleScript Class = "has_lifecycle_script"
	ClassPureJS             Class = "pure_js"
)

// Classification holds the result of classifying a tarball.
type Classification struct {
	// Class is the highest-precedence class matched.
	Class Class
	// Reason is a human-readable explanation of why this class was assigned.
	// Always non-empty.
	Reason string
	// RuleMatched is a stable dotted identifier (e.g. "suspicious.curl_pipe_sh",
	// "native.binding_gyp"). Empty only for pure_js.
	RuleMatched string
	// Version is the classifier version that produced this result.
	Version string
}

// PackageJSON holds the fields extracted from a package.json that are relevant
// to classification. Only the subset needed by the rules is parsed.
type PackageJSON struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Scripts map[string]string `json:"scripts"`
}

// installScriptKeys are the lifecycle scripts checked by the suspicious /
// fetches-at-install / has-lifecycle rules. Only install-time scripts count;
// test or build scripts are explicitly excluded.
var installScriptKeys = []string{"preinstall", "install", "postinstall"}

// maxFileSize is the threshold below which file contents are materialised into
// memory. Files larger than this are tracked by path only (content not stored).
const maxFileSize = 256 * 1024 // 256 KB

// Classify reads the npm tarball stream exactly once and returns the
// highest-precedence Classification. The stream must be a gzip-compressed tar
// archive (i.e. a standard npm .tgz). The caller must not seek the reader.
func Classify(tarball io.Reader) (Classification, error) {
	files, pkg, unpackErr := unpack(tarball)
	rules := []func() (Classification, bool){
		func() (Classification, bool) { return ruleBroken(files, pkg, unpackErr) },
		func() (Classification, bool) { return ruleSuspicious(files, pkg) },
		func() (Classification, bool) { return ruleHasNativeCode(files) },
		func() (Classification, bool) { return ruleFetchesAtInstall(pkg) },
		func() (Classification, bool) { return ruleHasLifecycleScript(pkg) },
	}
	for _, rule := range rules {
		if c, matched := rule(); matched {
			c.Version = Version
			return c, nil
		}
	}
	return Classification{
		Class:       ClassPureJS,
		Reason:      "no lifecycle scripts, no native files, no install-time fetches",
		RuleMatched: "",
		Version:     Version,
	}, nil
}

// unpack gunzips and untars the reader. It returns:
//   - files: a map from tarball path → file contents for files ≤ maxFileSize.
//     Larger files are present as keys with nil values (path tracked, no content).
//   - pkg: parsed package.json, or nil if missing/invalid.
//   - err: non-nil if the gzip or tar stream is malformed.
//
// The map keys preserve the original tarball paths (e.g. "package/package.json").
func unpack(r io.Reader) (files map[string][]byte, pkg *PackageJSON, err error) {
	files = make(map[string][]byte)

	gz, err := gzip.NewReader(r)
	if err != nil {
		return files, nil, fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return files, nil, fmt.Errorf("tar: %w", err)
		}
		// Skip non-regular files (directories, symlinks, etc.).
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		path := hdr.Name
		// Trim leading ./ or package/ prefix commonly added by npm pack.
		path = cleanTarPath(path)

		if hdr.Size > maxFileSize {
			// Track the path but don't read the content.
			files[path] = nil
			// Drain so the tar reader advances.
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return files, nil, fmt.Errorf("tar drain %s: %w", hdr.Name, err)
			}
			continue
		}

		content, err := io.ReadAll(tr)
		if err != nil {
			return files, nil, fmt.Errorf("tar read %s: %w", hdr.Name, err)
		}
		files[path] = content
	}

	// Parse package.json. npm packs it at "package/package.json" (which we
	// normalise to "package.json" via cleanTarPath).
	if raw, ok := files["package.json"]; ok && raw != nil {
		var p PackageJSON
		if err := json.Unmarshal(raw, &p); err != nil {
			// Return files so ruleBroken can report the exact error.
			return files, nil, &jsonParseError{err: err}
		}
		pkg = &p
	}

	return files, pkg, nil
}

// cleanTarPath normalises a tarball entry path:
//   - strips a leading "./" or "package/" component
//   - cleans double slashes
//
// npm tarballs conventionally wrap all files under "package/".
func cleanTarPath(p string) string {
	p = filepath.ToSlash(p)
	for _, prefix := range []string{"./package/", "package/", "./"} {
		if strings.HasPrefix(p, prefix) {
			return p[len(prefix):]
		}
	}
	return p
}

// jsonParseError wraps a json.Unmarshal error so ruleBroken can distinguish it
// from an unpack error.
type jsonParseError struct{ err error }

func (e *jsonParseError) Error() string { return fmt.Sprintf("package.json invalid JSON: %v", e.err) }
func (e *jsonParseError) Unwrap() error { return e.err }
