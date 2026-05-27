package classify

import (
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// openFixture opens a fixture tarball from testdata/<subdir>/<file>.
// The test is skipped if the file does not exist.
func openFixture(t *testing.T, subdir, file string) *os.File {
	t.Helper()
	path := "testdata/" + subdir + "/" + file
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("fixture %s not found (run testdata/build-fixtures.sh to generate it)", path)
		}
		t.Fatalf("open fixture %s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// classifyFixture calls Classify on the given fixture and returns the result.
func classifyFixture(t *testing.T, subdir, file string) Classification {
	t.Helper()
	f := openFixture(t, subdir, file)
	c, err := Classify(f)
	if err != nil {
		t.Fatalf("Classify(%s/%s): unexpected error: %v", subdir, file, err)
	}
	return c
}

// assertClass fails the test if got.Class != want or got.RuleMatched != wantRule.
// wantRule may be "" to skip the rule check.
func assertClass(t *testing.T, got Classification, want Class, wantRule string) {
	t.Helper()
	if got.Class != want {
		t.Errorf("Class: got %q, want %q (reason: %s)", got.Class, want, got.Reason)
	}
	if wantRule != "" && got.RuleMatched != wantRule {
		t.Errorf("RuleMatched: got %q, want %q", got.RuleMatched, wantRule)
	}
	if got.Version == "" {
		t.Errorf("Version: got empty string, want %q", Version)
	}
	if got.Reason == "" {
		t.Errorf("Reason: got empty string")
	}
}

// ---------------------------------------------------------------------------
// Fixture-driven tests — one per class
// ---------------------------------------------------------------------------

// --- pure_js ---

func TestClassify_PureJS_LeftPad(t *testing.T) {
	c := classifyFixture(t, "pure-js", "left-pad-1.3.0.tgz")
	assertClass(t, c, ClassPureJS, "")
	if c.RuleMatched != "" {
		t.Errorf("RuleMatched: got %q, want empty string for pure_js", c.RuleMatched)
	}
}

func TestClassify_PureJS_IsArray(t *testing.T) {
	c := classifyFixture(t, "pure-js", "is-array-1.0.1.tgz")
	assertClass(t, c, ClassPureJS, "")
}

// --- has_lifecycle_script ---

func TestClassify_HasLifecycle_Postinstall(t *testing.T) {
	c := classifyFixture(t, "has-lifecycle", "postinstall-echo-1.0.0.tgz")
	assertClass(t, c, ClassHasLifecycleScript, "lifecycle.postinstall")
}

func TestClassify_HasLifecycle_Preinstall(t *testing.T) {
	c := classifyFixture(t, "has-lifecycle", "preinstall-check-1.0.0.tgz")
	assertClass(t, c, ClassHasLifecycleScript, "lifecycle.preinstall")
}

// --- has_native_code ---

func TestClassify_HasNative_BindingGyp(t *testing.T) {
	c := classifyFixture(t, "has-native", "stub-native-1.0.0.tgz")
	assertClass(t, c, ClassHasNativeCode, "native.binding_gyp")
}

func TestClassify_HasNative_NodeFile(t *testing.T) {
	c := classifyFixture(t, "has-native", "prebuilt-addon-1.0.0.tgz")
	assertClass(t, c, ClassHasNativeCode, "native.source_file")
}

// --- fetches_at_install ---

func TestClassify_FetchesAtInstall_PreGypHelper(t *testing.T) {
	c := classifyFixture(t, "fetches-at-install", "fetch-prebuilt-1.0.0.tgz")
	assertClass(t, c, ClassFetchesAtInstall, "fetches.prebuilt_helper")
}

func TestClassify_FetchesAtInstall_URLLiteral(t *testing.T) {
	c := classifyFixture(t, "fetches-at-install", "url-installer-1.0.0.tgz")
	assertClass(t, c, ClassFetchesAtInstall, "fetches.url_literal")
}

// --- suspicious ---

func TestClassify_Suspicious_CurlPipeSh(t *testing.T) {
	c := classifyFixture(t, "suspicious", "curl-pipe-sh-1.0.0.tgz")
	assertClass(t, c, ClassSuspicious, "suspicious.curl_pipe_sh")
}

func TestClassify_Suspicious_WgetPipeSh(t *testing.T) {
	c := classifyFixture(t, "suspicious", "wget-pipe-sh-1.0.0.tgz")
	assertClass(t, c, ClassSuspicious, "suspicious.wget_pipe_sh")
}

func TestClassify_Suspicious_EvalDecoded(t *testing.T) {
	c := classifyFixture(t, "suspicious", "eval-atob-1.0.0.tgz")
	assertClass(t, c, ClassSuspicious, "suspicious.eval_decoded")
}

func TestClassify_Suspicious_PathSecretRef(t *testing.T) {
	c := classifyFixture(t, "suspicious", "path-secret-ref-1.0.0.tgz")
	assertClass(t, c, ClassSuspicious, "suspicious.path_secret_ref")
}

func TestClassify_Suspicious_ExfilEnvVar(t *testing.T) {
	c := classifyFixture(t, "suspicious", "exfil-env-var-1.0.0.tgz")
	assertClass(t, c, ClassSuspicious, "suspicious.exfil_env_var")
}

// --- broken ---

func TestClassify_Broken_TruncatedGzip(t *testing.T) {
	c := classifyFixture(t, "broken", "truncated-gzip.tgz")
	assertClass(t, c, ClassBroken, "broken")
	if !strings.Contains(c.Reason, "tarball failed to unpack") {
		t.Errorf("Reason: got %q, want it to contain %q", c.Reason, "tarball failed to unpack")
	}
}

func TestClassify_Broken_InvalidJSON(t *testing.T) {
	c := classifyFixture(t, "broken", "invalid-json-package.tgz")
	assertClass(t, c, ClassBroken, "broken")
	if !strings.Contains(c.Reason, "package.json invalid JSON") {
		t.Errorf("Reason: got %q, want it to contain %q", c.Reason, "package.json invalid JSON")
	}
}

func TestClassify_Broken_MissingPackageJSON(t *testing.T) {
	c := classifyFixture(t, "broken", "missing-package-json.tgz")
	assertClass(t, c, ClassBroken, "broken")
	if c.Reason != "package.json missing" {
		t.Errorf("Reason: got %q, want %q", c.Reason, "package.json missing")
	}
}

func TestClassify_Broken_MissingName(t *testing.T) {
	c := classifyFixture(t, "broken", "missing-name.tgz")
	assertClass(t, c, ClassBroken, "broken")
	if c.Reason != "package.json missing name" {
		t.Errorf("Reason: got %q, want %q", c.Reason, "package.json missing name")
	}
}

// ---------------------------------------------------------------------------
// Negative-case table tests
// ---------------------------------------------------------------------------

// TestNegativeCases verifies that rules do NOT fire on ambiguous inputs that
// should fall through to a lower-precedence class or pure_js.
func TestNegativeCases(t *testing.T) {
	cases := []struct {
		name     string
		pkg      PackageJSON
		files    map[string][]byte
		wantNot  Class // the class that should NOT be returned
		wantIs   Class // the class that SHOULD be returned
		wantRule string
	}{
		{
			// binding.gyp mentioned in a README should not trigger has_native_code.
			// Only a real binding.gyp *file* in the tarball triggers the rule.
			name: "binding_gyp_in_readme_not_native",
			pkg: PackageJSON{
				Name:    "readme-mentions-gyp",
				Scripts: map[string]string{},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"readme-mentions-gyp","version":"1.0.0"}`),
				"README.md":    []byte("This package does not use binding.gyp or any native code."),
			},
			wantNot: ClassHasNativeCode,
			wantIs:  ClassPureJS,
		},
		{
			// https:// in a *test* script should not trigger fetches_at_install
			// because only install-time scripts (preinstall/install/postinstall) are checked.
			name: "https_in_test_script_not_fetches",
			pkg: PackageJSON{
				Name: "test-uses-url",
				Scripts: map[string]string{
					"test": "node -e \"require('https').get('https://example.com', ()=>{})\"",
				},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"test-uses-url","version":"1.0.0"}`),
				"index.js":     []byte("module.exports = {};"),
			},
			wantNot: ClassFetchesAtInstall,
			wantIs:  ClassPureJS,
		},
		{
			// https:// commented out in an install script should not trigger
			// fetches_at_install (comment stripping removes it).
			name: "https_in_comment_stripped",
			pkg: PackageJSON{
				Name: "commented-url",
				Scripts: map[string]string{
					"install": "echo 'installing' # https://example.com/this-is-a-comment",
				},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"commented-url","version":"1.0.0"}`),
			},
			wantNot:  ClassFetchesAtInstall,
			wantIs:   ClassHasLifecycleScript,
			wantRule: "lifecycle.install",
		},
		{
			// A whitespace-only install script should not trigger has_lifecycle_script.
			name: "whitespace_install_script_not_lifecycle",
			pkg: PackageJSON{
				Name: "whitespace-script",
				Scripts: map[string]string{
					"install": "   \t  ",
				},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"whitespace-script","version":"1.0.0"}`),
			},
			wantNot: ClassHasLifecycleScript,
			wantIs:  ClassPureJS,
		},
		{
			// A .c file mentioned in package.json scripts (not as a tarball entry)
			// should not trigger has_native_code. Only actual tarball file paths matter.
			name: "c_file_in_script_not_native",
			pkg: PackageJSON{
				Name: "builds-c-externally",
				Scripts: map[string]string{
					"build": "gcc -o out main.c",
				},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"builds-c-externally","version":"1.0.0"}`),
				"index.js":     []byte("module.exports = {};"),
			},
			wantNot: ClassHasNativeCode,
			wantIs:  ClassPureJS,
		},
		{
			// AWS_SECRET_ACCESS_KEY in a *build* script (not an install script)
			// should not trigger suspicious.
			name: "aws_env_var_in_build_script_not_suspicious",
			pkg: PackageJSON{
				Name: "aws-deploy-tool",
				Scripts: map[string]string{
					"deploy": "aws s3 cp dist/ s3://bucket/ --region $AWS_DEFAULT_REGION",
				},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"aws-deploy-tool","version":"1.0.0"}`),
				"index.js":     []byte("module.exports = {};"),
			},
			wantNot: ClassSuspicious,
			wantIs:  ClassPureJS,
		},
		{
			// curl in a test script — not suspicious (only install scripts checked)
			name: "curl_in_test_script_not_suspicious",
			pkg: PackageJSON{
				Name: "test-curl",
				Scripts: map[string]string{
					"test": "curl https://httpbin.org/get | sh",
				},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"test-curl","version":"1.0.0"}`),
				"index.js":     []byte("module.exports = {};"),
			},
			wantNot: ClassSuspicious,
			wantIs:  ClassPureJS,
		},
		{
			// node-pre-gyp in a *build* script should NOT trigger fetches_at_install.
			name: "pre_gyp_in_build_script_not_fetches",
			pkg: PackageJSON{
				Name: "build-only-gyp",
				Scripts: map[string]string{
					"build": "node-pre-gyp build",
				},
			},
			files: map[string][]byte{
				"package.json": []byte(`{"name":"build-only-gyp","version":"1.0.0"}`),
				"index.js":     []byte("module.exports = {};"),
			},
			wantNot: ClassFetchesAtInstall,
			wantIs:  ClassPureJS,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run just the relevant rules directly (unit test the rule functions)
			// rather than building a tarball, so we can use Go structs directly.

			// Verify the "wantNot" rule does not fire.
			switch tc.wantNot {
			case ClassSuspicious:
				if c, ok := ruleSuspicious(tc.files, &tc.pkg); ok {
					t.Errorf("ruleSuspicious unexpectedly fired: class=%s rule=%s reason=%s",
						c.Class, c.RuleMatched, c.Reason)
				}
			case ClassHasNativeCode:
				if c, ok := ruleHasNativeCode(tc.files); ok {
					t.Errorf("ruleHasNativeCode unexpectedly fired: class=%s rule=%s reason=%s",
						c.Class, c.RuleMatched, c.Reason)
				}
			case ClassFetchesAtInstall:
				if c, ok := ruleFetchesAtInstall(&tc.pkg); ok {
					t.Errorf("ruleFetchesAtInstall unexpectedly fired: class=%s rule=%s reason=%s",
						c.Class, c.RuleMatched, c.Reason)
				}
			case ClassHasLifecycleScript:
				if c, ok := ruleHasLifecycleScript(&tc.pkg); ok {
					t.Errorf("ruleHasLifecycleScript unexpectedly fired: class=%s rule=%s reason=%s",
						c.Class, c.RuleMatched, c.Reason)
				}
			}

			// Run the full rule chain to verify the expected class is assigned.
			// We synthesise a minimal Classification by running each rule in order.
			var got Classification
			var matched bool

			// broken: skip (no unpackErr in these tests)
			if !matched {
				got, matched = ruleSuspicious(tc.files, &tc.pkg)
			}
			if !matched {
				got, matched = ruleHasNativeCode(tc.files)
			}
			if !matched {
				got, matched = ruleFetchesAtInstall(&tc.pkg)
			}
			if !matched {
				got, matched = ruleHasLifecycleScript(&tc.pkg)
			}
			if !matched {
				got = Classification{
					Class:       ClassPureJS,
					Reason:      "no lifecycle scripts, no native files, no install-time fetches",
					RuleMatched: "",
					Version:     Version,
				}
			} else {
				got.Version = Version
			}

			if got.Class != tc.wantIs {
				t.Errorf("Class: got %q, want %q (reason: %s)", got.Class, tc.wantIs, got.Reason)
			}
			if tc.wantRule != "" && got.RuleMatched != tc.wantRule {
				t.Errorf("RuleMatched: got %q, want %q", got.RuleMatched, tc.wantRule)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Unit tests for internal helpers
// ---------------------------------------------------------------------------

func TestStripShellComments(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: "echo hello # this is a comment",
			want:  "echo hello ",
		},
		{
			input: "echo hello",
			want:  "echo hello",
		},
		{
			input: "# full comment line",
			want:  "",
		},
		{
			input: "line1\n# comment\nline3 # inline",
			want:  "line1\n\nline3 ",
		},
		{
			// https:// after # should be stripped
			input: "echo ok # https://example.com should not be seen",
			want:  "echo ok ",
		},
	}
	for _, tc := range cases {
		got := stripShellComments(tc.input)
		if got != tc.want {
			t.Errorf("stripShellComments(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCleanTarPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"package/package.json", "package.json"},
		{"./package/index.js", "index.js"},
		{"./index.js", "index.js"},
		{"package/lib/util.js", "lib/util.js"},
		{"binding.gyp", "binding.gyp"},
	}
	for _, tc := range cases {
		got := cleanTarPath(tc.input)
		if got != tc.want {
			t.Errorf("cleanTarPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestExcerpt(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := excerpt(long)
	if len(got) > 83 { // 80 chars + "…" (3 bytes in UTF-8)
		t.Errorf("excerpt(%d chars) returned %d chars, want ≤ 83", len(long), len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("excerpt of long string should end with '…', got %q", got)
	}

	short := "hello"
	if got := excerpt(short); got != short {
		t.Errorf("excerpt(%q) = %q, want %q", short, got, short)
	}
}

// ---------------------------------------------------------------------------
// Version constant test
// ---------------------------------------------------------------------------

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Fatal("Version constant is empty")
	}
	// Verify Classify doesn't panic on empty input; result is discarded.
	_, _ = Classify(strings.NewReader(""))
	// The substantive assertion happens against a real fixture.
	f := openFixture(t, "pure-js", "left-pad-1.3.0.tgz")
	c, err := Classify(f)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if c.Version != Version {
		t.Errorf("Classification.Version: got %q, want %q", c.Version, Version)
	}
}
