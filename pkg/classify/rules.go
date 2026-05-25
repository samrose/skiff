package classify

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Compiled regexes — compiled once at package init, not per call.
// ---------------------------------------------------------------------------

var (
	// suspicious: curl ... | sh
	reCurlPipeSh = regexp.MustCompile(`curl\s+[^|]*\|\s*(sudo\s+)?sh`)
	// suspicious: wget ... | sh
	reWgetPipeSh = regexp.MustCompile(`wget\s+[^|]*\|\s*(sudo\s+)?sh`)
	// suspicious: eval(atob(...)) or eval(Buffer.from(...))
	reEvalDecoded = regexp.MustCompile(`eval\s*\(\s*(atob|Buffer\.from)\s*\(`)
	// suspicious: env-var exfil patterns
	reExfilEnvVar = regexp.MustCompile(`\$\{?(AWS_[A-Z_]+|GITHUB_TOKEN|GH_TOKEN|NPM_TOKEN|GITLAB_TOKEN|DOCKER_PASSWORD|KUBE_TOKEN)\}?`)

	// native extensions and source file extensions
	nativeExtensions = map[string]bool{
		".node": true,
		".c":    true,
		".cc":   true,
		".cpp":  true,
		".cxx":  true,
		".m":    true,
		".mm":   true,
		".h":    true,
		".hpp":  true,
	}

	// fetches at install: pre-built helpers
	fetchHelpers = []string{"node-pre-gyp", "@mapbox/node-pre-gyp", "prebuild-install"}
)

// suspicious: literal secret-path references
var secretLiterals = []string{
	"/etc/passwd",
	"/etc/shadow",
	"~/.ssh",
	"~/.aws",
	"$HOME/.ssh",
	"$HOME/.aws",
}

// ---------------------------------------------------------------------------
// Rule: broken
// ---------------------------------------------------------------------------

// ruleBroken fires when the tarball could not be unpacked, package.json is
// missing, package.json is invalid JSON, or the name field is empty.
func ruleBroken(files map[string][]byte, pkg *PackageJSON, unpackErr error) (Classification, bool) {
	if unpackErr != nil {
		var reason string
		if jpe, ok := unpackErr.(*jsonParseError); ok {
			reason = jpe.Error()
		} else {
			reason = fmt.Sprintf("tarball failed to unpack: %v", unpackErr)
		}
		return Classification{
			Class:       ClassBroken,
			Reason:      reason,
			RuleMatched: "broken",
		}, true
	}

	if _, ok := files["package.json"]; !ok {
		return Classification{
			Class:       ClassBroken,
			Reason:      "package.json missing",
			RuleMatched: "broken",
		}, true
	}

	if pkg == nil {
		// files["package.json"] exists (checked above) but pkg is nil means
		// it couldn't be parsed — unpackErr should have been set. This is a
		// defensive fallback.
		return Classification{
			Class:       ClassBroken,
			Reason:      "package.json missing or unparseable",
			RuleMatched: "broken",
		}, true
	}

	if strings.TrimSpace(pkg.Name) == "" {
		return Classification{
			Class:       ClassBroken,
			Reason:      "package.json missing name",
			RuleMatched: "broken",
		}, true
	}

	return Classification{}, false
}

// ---------------------------------------------------------------------------
// Rule: suspicious
// ---------------------------------------------------------------------------

// ruleSuspicious fires when any install-time lifecycle script contains patterns
// associated with credential theft, code execution from remote URLs, or
// exfiltration of secrets.
func ruleSuspicious(_ map[string][]byte, pkg *PackageJSON) (Classification, bool) {
	if pkg == nil {
		return Classification{}, false
	}

	for _, scriptName := range installScriptKeys {
		script, ok := pkg.Scripts[scriptName]
		if !ok || script == "" {
			continue
		}

		if m := reCurlPipeSh.FindString(script); m != "" {
			return Classification{
				Class:       ClassSuspicious,
				Reason:      fmt.Sprintf("install script %q contains curl pipe to shell: %s", scriptName, excerpt(m)),
				RuleMatched: "suspicious.curl_pipe_sh",
			}, true
		}

		if m := reWgetPipeSh.FindString(script); m != "" {
			return Classification{
				Class:       ClassSuspicious,
				Reason:      fmt.Sprintf("install script %q contains wget pipe to shell: %s", scriptName, excerpt(m)),
				RuleMatched: "suspicious.wget_pipe_sh",
			}, true
		}

		if m := reEvalDecoded.FindString(script); m != "" {
			return Classification{
				Class:       ClassSuspicious,
				Reason:      fmt.Sprintf("install script %q contains base64-decoded eval: %s", scriptName, excerpt(m)),
				RuleMatched: "suspicious.eval_decoded",
			}, true
		}

		for _, lit := range secretLiterals {
			if strings.Contains(script, lit) {
				return Classification{
					Class:       ClassSuspicious,
					Reason:      fmt.Sprintf("install script %q references secret path %q", scriptName, lit),
					RuleMatched: "suspicious.path_secret_ref",
				}, true
			}
		}

		if m := reExfilEnvVar.FindString(script); m != "" {
			return Classification{
				Class:       ClassSuspicious,
				Reason:      fmt.Sprintf("install script %q references secret environment variable: %s", scriptName, excerpt(m)),
				RuleMatched: "suspicious.exfil_env_var",
			}, true
		}
	}

	return Classification{}, false
}

// ---------------------------------------------------------------------------
// Rule: has_native_code
// ---------------------------------------------------------------------------

// ruleHasNativeCode fires when the tarball contains a binding.gyp at the
// package root, or any file with a native source or compiled extension.
// binding.gyp is checked first (deterministically) before iterating the map.
func ruleHasNativeCode(files map[string][]byte) (Classification, bool) {
	// Check binding.gyp first — this is deterministic regardless of map iteration order.
	if _, ok := files["binding.gyp"]; ok {
		return Classification{
			Class:       ClassHasNativeCode,
			Reason:      "tarball contains binding.gyp at root: binding.gyp",
			RuleMatched: "native.binding_gyp",
		}, true
	}

	// Check for native source/compiled file extensions.
	for path := range files {
		ext := strings.ToLower(filepath.Ext(path))
		if nativeExtensions[ext] {
			return Classification{
				Class:       ClassHasNativeCode,
				Reason:      fmt.Sprintf("tarball contains native source file: %s", path),
				RuleMatched: "native.source_file",
			}, true
		}
	}

	return Classification{}, false
}

// ---------------------------------------------------------------------------
// Rule: fetches_at_install
// ---------------------------------------------------------------------------

// ruleFetchesAtInstall fires when install-time lifecycle scripts reference
// pre-built helper tools (node-pre-gyp, prebuild-install) or contain literal
// HTTP/HTTPS URLs. Comment lines (after #) are stripped before matching.
func ruleFetchesAtInstall(pkg *PackageJSON) (Classification, bool) {
	if pkg == nil {
		return Classification{}, false
	}

	for _, scriptName := range installScriptKeys {
		script, ok := pkg.Scripts[scriptName]
		if !ok || script == "" {
			continue
		}

		stripped := stripShellComments(script)

		for _, helper := range fetchHelpers {
			if strings.Contains(stripped, helper) {
				return Classification{
					Class:       ClassFetchesAtInstall,
					Reason:      fmt.Sprintf("install script %q references pre-built helper %q", scriptName, helper),
					RuleMatched: "fetches.prebuilt_helper",
				}, true
			}
		}

		for _, scheme := range []string{"http://", "https://"} {
			if strings.Contains(stripped, scheme) {
				// Find the excerpt for the reason message.
				idx := strings.Index(stripped, scheme)
				end := idx + 60
				if end > len(stripped) {
					end = len(stripped)
				}
				return Classification{
					Class:       ClassFetchesAtInstall,
					Reason:      fmt.Sprintf("install script %q contains URL literal: %s", scriptName, excerpt(stripped[idx:end])),
					RuleMatched: "fetches.url_literal",
				}, true
			}
		}
	}

	return Classification{}, false
}

// ---------------------------------------------------------------------------
// Rule: has_lifecycle_script
// ---------------------------------------------------------------------------

// ruleHasLifecycleScript fires when package.json declares a non-empty
// preinstall, install, or postinstall script. This is the lowest-precedence
// rule; it fires only when no higher-precedence rule matched.
func ruleHasLifecycleScript(pkg *PackageJSON) (Classification, bool) {
	if pkg == nil {
		return Classification{}, false
	}

	for _, scriptName := range installScriptKeys {
		val, ok := pkg.Scripts[scriptName]
		if !ok {
			continue
		}
		if strings.TrimSpace(val) == "" {
			// Treat whitespace-only as not set.
			continue
		}
		return Classification{
			Class:       ClassHasLifecycleScript,
			Reason:      fmt.Sprintf("package.json has lifecycle script %q", scriptName),
			RuleMatched: fmt.Sprintf("lifecycle.%s", scriptName),
		}, true
	}

	return Classification{}, false
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stripShellComments removes everything after a '#' character on each line.
// This is intentionally naive — it does not try to handle '#' inside JS string
// literals. The plan explicitly accepts this limitation (task 3.2, constraint 4).
func stripShellComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "#"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// excerpt returns a short excerpt suitable for use in a reason string.
// It caps the string at 80 characters and adds "…" if truncated.
func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
