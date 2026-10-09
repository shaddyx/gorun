package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const usage = `gorun - run a Go application from a git URL

Usage:
  gorun [flags] <git-url> [app-args...]

Flags:
  --upgrade      force re-fetch (git pull) and rebuild, then run
  --upgrade-all  re-query git and rebuild every cached project, then exit
  --clean        wipe the entire gorun cache, then exit
  --verbose      show the full process output without suppression
  --main <dir>   module-relative path to the main package to build
                 (overrides the subdirectory implied by a /tree/ URL)
  --install      build (or reuse) the target's binary and install it to
                 $GOBIN (or $(go env GOPATH)/bin), then exit

The first positional argument is the git URL; everything after it is
forwarded verbatim to the application.

By default the main package is auto-detected: a repo whose binary lives in a
subdirectory (e.g. cmd/<name>) is located and built automatically. If several
main packages exist, or to pin a specific one, pass --main <dir>:
  gorun --main cmd/lazy-skill-mcp github.com/shaddyx/lazy-skills-mcp

A /tree/<branch>/<path> URL (a "browse directory" link) points at a
subdirectory of the repo. The repo is cloned at that branch and the main
package in <path> is built — a <path> with its own go.mod is built as a
nested module. The binary is named after the last <path> segment:
  gorun github.com/MegaFair/mf-ai-tools/tree/main/mcp-servers/grafana-logs-downloader
All branches of the same bundle share one cache entry, like @latest.

Pin a version with an @ref suffix, recognized only after the first '/':
  gorun github.com/user/repo@v1.0.2   # exact tag
  gorun github.com/user/repo@main     # track a branch
  gorun github.com/user/repo@latest   # highest semver tag, or default branch
Clones use --depth 1; each resolved version gets its own cache entry.
`

func main() {
	upgrade := flag.Bool("upgrade", false, "force re-fetch and rebuild, then run")
	upgradeAll := flag.Bool("upgrade-all", false, "re-query git and rebuild every cached project")
	clean := flag.Bool("clean", false, "wipe the entire gorun cache")
	verbose := flag.Bool("verbose", false, "show the full process output without suppression")
	mainDir := flag.String("main", "", "module-relative path to the main package to build (default: auto-detect)")
	install := flag.Bool("install", false, "build and install the app binary instead of running it")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	args := flag.Args()
	if err := validateFlags(*install, *clean, *upgradeAll, args); err != nil {
		fatal("%v", err)
	}

	cacheRoot, err := cacheRoot()
	if err != nil {
		fatal("%v", err)
	}

	if *clean {
		if err := os.RemoveAll(cacheRoot); err != nil {
			fatal("clean: %v", err)
		}
		fmt.Printf("cleaned %s\n", cacheRoot)
		return
	}

	if *upgradeAll {
		if err := upgradeAllCached(cacheRoot, *verbose); err != nil {
			fatal("%v", err)
		}
		return
	}

	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	url := args[0]
	appArgs := args[1:]

	if err := run(cacheRoot, url, appArgs, *upgrade, *verbose, *mainDir, *install); err != nil {
		fatal("%v", err)
	}
}

// validateFlags reports invalid flag combinations before any work is done.
func validateFlags(install, clean, upgradeAll bool, args []string) error {
	if clean && upgradeAll {
		return errors.New("--clean and --upgrade-all are mutually exclusive")
	}
	if install && (clean || upgradeAll) {
		return errors.New("--install cannot be combined with --clean or --upgrade-all")
	}
	if install && len(args) > 1 {
		return errors.New("--install does not forward arguments to the app")
	}
	return nil
}

func run(cacheRoot, url string, appArgs []string, upgrade, verbose bool, mainDir string, install bool) error {
	repoURL, ref, subPath := splitTarget(url)

	// The cache key uses the literal ref ("latest" stays "latest"), so @latest
	// always maps to the same stable cache dir regardless of the resolved
	// version. /tree/ URLs are keyed on the base repo URL alone, so every
	// branch of the same bundle shares one stable cache entry.
	key := cacheKey(keySource(repoURL, ref, subPath))
	name := targetName(repoURL, subPath)
	dir := filepath.Join(cacheRoot, name+"-"+key)
	srcDir := filepath.Join(dir, "src")
	binDir := filepath.Join(dir, "bin")
	binPath := filepath.Join(binDir, name)

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "url"), []byte(url), 0o644); err != nil {
		return err
	}

	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		// First run: no cache, so @latest must be resolved to a concrete ref.
		cloneRef := ref
		if ref == "latest" {
			resolved, rerr := resolveLatestRef(url, repoURL)
			if rerr != nil {
				return rerr
			}
			cloneRef = resolved
		}
		if err := gitClone(repoURL, cloneRef, srcDir, verbose); err != nil {
			return err
		}
	} else if upgrade {
		// Cached: only re-resolve @latest on an explicit --upgrade.
		pullRef := ref
		if ref == "latest" {
			resolved, rerr := resolveLatestRef(url, repoURL)
			if rerr != nil {
				return rerr
			}
			pullRef = resolved
		}
		if err := gitPull(srcDir, pullRef, verbose); err != nil {
			return err
		}
	}

	needBuild := upgrade
	if !needBuild {
		if _, err := os.Stat(binPath); os.IsNotExist(err) {
			needBuild = true
		}
	}

	if needBuild {
		persisted := readBuildPath(dir)
		explicit := subPath
		if mainDir != "" {
			explicit = mainDir
		}
		pkg, err := pickMainPackage(srcDir, explicit, persisted)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "buildpath"), []byte(pkg), 0o644); err != nil {
			return err
		}
		if err := goBuild(srcDir, binPath, pkg); err != nil {
			return err
		}
	}

	if install {
		dst, err := installApp(binPath, name)
		if err != nil {
			return err
		}
		fmt.Printf("installed %s\n", dst)
		return nil
	}
	return execApp(binPath, appArgs)
}

func upgradeAllCached(cacheRoot string, verbose bool) error {
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no cached projects")
			return nil
		}
		return err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(cacheRoot, e.Name())
		srcDir := filepath.Join(dir, "src")
		binDir := filepath.Join(dir, "bin")

		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			continue
		}
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return err
		}

		ref := ""
		subPath := ""
		name := ""
		if b, err := os.ReadFile(filepath.Join(dir, "url")); err == nil {
			raw := strings.TrimSpace(string(b))
			if baseRepo, treeRef, sub, isTree := parseTreeURL(raw); isTree {
				name = targetName(normalizeURL(baseRepo), sub)
				ref = treeRef
				subPath = sub
			} else {
				repoURL, r := parseRef(raw)
				repoURL = normalizeURL(repoURL)
				if r == "latest" {
					resolved, rerr := resolveLatest(repoURL)
					if rerr != nil {
						return fmt.Errorf("resolving @latest for %s: %w", e.Name(), rerr)
					}
					ref = resolved
				} else {
					ref = r
				}
			}
		}
		if name == "" {
			name = repoNameFromDir(srcDir)
		}
		binPath := filepath.Join(binDir, name)

		fmt.Printf("upgrading %s\n", e.Name())
		if err := gitPull(srcDir, ref, verbose); err != nil {
			return err
		}
		persisted := readBuildPath(dir)
		pkg, perr := pickMainPackage(srcDir, subPath, persisted)
		if perr != nil {
			return perr
		}
		if err := os.WriteFile(filepath.Join(dir, "buildpath"), []byte(pkg), 0o644); err != nil {
			return err
		}
		if err := goBuild(srcDir, binPath, pkg); err != nil {
			return err
		}
	}
	return nil
}

func gitClone(url, ref, srcDir string, verbose bool) error {
	fmt.Printf("cloning %s\n", url)
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, srcDir)
	cmd := exec.Command("git", args...)
	return runGit(cmd, verbose)
}

func gitPull(srcDir, ref string, verbose bool) error {
	fmt.Printf("pulling %s\n", srcDir)
	cmd := exec.Command("git", "-C", srcDir, "fetch", "--all", "--prune", "--tags")
	if err := runGit(cmd, verbose); err != nil {
		return err
	}
	target := ref
	switch {
	case target == "":
		target = "origin/HEAD"
	case hasRemoteBranch(srcDir, target):
		// A pinned branch: reset to the freshly fetched remote-tracking ref.
		// A bare branch name would resolve to the stale local branch.
		target = "origin/" + target
	}
	cmd = exec.Command("git", "-C", srcDir, "reset", "--hard", target)
	return runGit(cmd, verbose)
}

// hasRemoteBranch reports whether ref names a branch on the origin remote.
func hasRemoteBranch(srcDir, ref string) bool {
	out, err := exec.Command("git", "-C", srcDir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+ref).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// runGit runs cmd, streaming output when verbose, otherwise capturing it and
// only printing it if the command fails.
func runGit(cmd *exec.Cmd, verbose bool) error {
	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return runQuiet(cmd)
}

// runQuiet runs cmd with output captured, only printing it if the command fails.
func runQuiet(cmd *exec.Cmd) error {
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		fmt.Fprint(os.Stderr, buf.String())
		return err
	}
	return nil
}

func goBuild(srcDir, binPath, pkg string) error {
	buildDir := srcDir
	if pkg != "" {
		buildDir = filepath.Join(srcDir, filepath.FromSlash(pkg))
	}
	fmt.Printf("building %s\n", binPath)
	// Build "." inside the target package so a subdirectory with its own
	// go.mod (a nested module) is built in its own module context.
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = buildDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// normalizePkgPath normalizes a module-relative package path like "./cmd/x" or
// "/cmd/x" into "cmd/x".
func normalizePkgPath(p string) string {
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimPrefix(p, "./")
	return strings.TrimSuffix(p, "/")
}

// readBuildPath reads the previously persisted main-package path, if any.
func readBuildPath(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "buildpath"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// pickMainPackage returns the module-relative path of the main package to build.
// An explicit path wins; otherwise a previously persisted path is reused if it is
// still valid; otherwise the main package is auto-detected. It returns "" when
// the module root is the main package.
func pickMainPackage(srcDir, explicit, persisted string) (string, error) {
	explicit = normalizePkgPath(explicit)
	persisted = normalizePkgPath(persisted)

	if explicit != "" {
		return validateMainPackageDir(srcDir, explicit)
	}
	if persisted != "" {
		if _, err := validateMainPackageDir(srcDir, persisted); err == nil {
			return persisted, nil
		}
	}

	dirs, err := findMainPackageDirs(srcDir)
	if err != nil {
		return "", err
	}
	switch {
	case len(dirs) == 0:
		return "", errors.New("no main package (package main) found in repository")
	case len(dirs) == 1:
		return dirs[0], nil
	}
	for _, d := range dirs {
		if d == "" {
			return "", nil
		}
	}
	return "", fmt.Errorf("multiple main packages found (%s); pick one with --main", strings.Join(dirs, ", "))
}

func validateMainPackageDir(srcDir, rel string) (string, error) {
	p := filepath.Join(srcDir, filepath.FromSlash(rel))
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("main package path %q is not a directory", rel)
	}
	if !isMainPackageDir(p) {
		return "", fmt.Errorf("main package path %q does not contain a main package", rel)
	}
	return rel, nil
}

// findMainPackageDirs returns the module-relative paths of every directory in
// srcDir that declares "package main" in a non-test .go file. The module root is
// represented by "". Hidden dirs, vendor/, and testdata/ are skipped. Results
// are sorted and de-duplicated.
func findMainPackageDirs(srcDir string) ([]string, error) {
	var dirs []string
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		if path != srcDir {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
		}
		if isMainPackageDir(path) {
			rel, relErr := filepath.Rel(srcDir, path)
			if relErr != nil {
				return nil
			}
			dirRel := ""
			if rel != "." {
				dirRel = filepath.ToSlash(rel)
			}
			dirs = append(dirs, dirRel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

func isMainPackageDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		if strings.HasPrefix(n, "_") || strings.HasPrefix(n, ".") {
			continue
		}
		if isMainPackageFile(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}

// isMainPackageFile reports whether the .go file's package clause is "main".
var packageClauseRe = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)`)

func isMainPackageFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := packageClauseRe.FindStringSubmatch(sc.Text())
		if m != nil {
			return m[1] == "main"
		}
	}
	return false
}

func execApp(binPath string, appArgs []string) error {
	cmd := exec.Command(binPath, appArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// goEnv returns the value of the go environment variable key, as reported by
// `go env`. Shelling out honors settings made with `go env -w`, which are not
// exported in the process environment, so we match what `go install` would do.
func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// installDir returns the directory that `go install` writes binaries to: $GOBIN
// when set, otherwise the bin subdirectory of the first GOPATH entry.
func installDir() (string, error) {
	gobin, err := goEnv("GOBIN")
	if err != nil {
		return "", err
	}
	if gobin != "" {
		if !filepath.IsAbs(gobin) {
			return "", fmt.Errorf("GOBIN must be an absolute path, got %q", gobin)
		}
		return gobin, nil
	}
	gopath, err := goEnv("GOPATH")
	if err != nil {
		return "", err
	}
	paths := filepath.SplitList(gopath)
	if len(paths) == 0 || paths[0] == "" {
		return "", errors.New("cannot determine install directory: GOBIN and GOPATH are both unset")
	}
	return filepath.Join(paths[0], "bin"), nil
}

// validInstallName reports whether name is safe to use as a file name in the
// install directory. The name is derived from a user-supplied URL, so rejecting
// path separators (both '/' and the Windows '\') keeps a crafted URL from
// escaping the install directory via path traversal.
func validInstallName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`)
}

// installApp copies the built binary at binPath into the install directory and
// returns the destination path. The .exe suffix is appended on Windows to match
// what `go install` produces.
func installApp(binPath, name string) (string, error) {
	if !validInstallName(name) {
		return "", fmt.Errorf("invalid install name %q", name)
	}
	dir, err := installDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating install dir %s: %w", dir, err)
	}
	dst := filepath.Join(dir, name)
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(dst), ".exe") {
		dst += ".exe"
	}
	if err := copyExecutable(binPath, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// copyExecutable copies src to dst by writing a temp file in dst's directory and
// renaming it into place, so an interrupted copy never leaves a partial binary
// at dst.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".gorun-install-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", dst, err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("copying %s: %w", src, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("setting permissions on %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("installing %s: %w", dst, err)
	}
	return nil
}

func cacheRoot() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "gorun"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "gorun"), nil
}

func cacheKey(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// normalizeURL prepends https:// to a schemeless URL (e.g. "github.com/user/repo")
// so git can clone it. URLs that already carry a scheme or use the git@ SCP form
// are returned unchanged.
func normalizeURL(url string) string {
	if strings.Contains(url, "://") || strings.HasPrefix(url, "git@") {
		return url
	}
	return "https://" + url
}

// parseRef splits a git URL on a trailing @ref (tag, branch, or commit SHA).
// The ref is only recognized when the '@' appears after the first '/', so SSH
// URLs (git@host:...) and https userinfo are left untouched.
func parseRef(url string) (string, string) {
	slash := strings.Index(url, "/")
	at := strings.LastIndex(url, "@")
	if slash >= 0 && at > slash {
		return url[:at], url[at+1:]
	}
	return url, ""
}

// parseTreeURL splits a git-host "tree" URL (the "browse directory" link form
// used by GitHub, GitLab, and Gitea) into the base repository URL, the ref
// shown in the URL, and the module-relative subdirectory:
//
//	github.com/o/r/tree/main/mcp-servers/tool
//	    -> baseRepo "github.com/o/r", ref "main", subPath "mcp-servers/tool"
//
// Trailing ?query and #fragment (browser copy artifacts) are stripped, and
// percent-encoded path segments are decoded (e.g. branch names containing
// '/'). ok is false for URLs without a /tree/ marker.
func parseTreeURL(url string) (baseRepo, ref, subPath string, ok bool) {
	u := url
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u = u[:i]
	}
	const marker = "/tree/"
	i := strings.Index(u, marker)
	if i < 0 {
		return "", "", "", false
	}
	baseRepo = u[:i]
	rest := u[i+len(marker):]
	var sub string
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		ref = decodeURISegment(rest[:slash])
		parts := strings.Split(rest[slash+1:], "/")
		for i := range parts {
			parts[i] = decodeURISegment(parts[i])
		}
		sub = strings.Join(parts, "/")
	} else {
		ref = decodeURISegment(rest)
	}
	sub = strings.TrimSuffix(sub, "/")
	if baseRepo == "" || ref == "" {
		return "", "", "", false
	}
	return baseRepo, ref, sub, true
}

// decodeURISegment percent-decodes a URL path segment, returning it unchanged
// if it is not valid percent-encoding.
func decodeURISegment(s string) string {
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}

// splitTarget splits a user-supplied URL into the repository to clone, an
// optional ref, and an optional module-relative subdirectory. A /tree/ URL
// pins all three; any other URL yields a repository and optional @ref only.
func splitTarget(u string) (repoURL, ref, subPath string) {
	if base, treeRef, sub, ok := parseTreeURL(u); ok {
		return normalizeURL(base), treeRef, sub
	}
	repo, r := parseRef(u)
	return normalizeURL(repo), r, ""
}

// keySource returns the string hashed into the cache key. /tree/ URLs are
// keyed on the base repository URL alone, so every branch of the same bundle
// shares one cache entry, like @latest. Plain URLs additionally include the
// ref, so each version gets its own cache entry.
func keySource(repoURL, ref, subPath string) string {
	if subPath != "" {
		return repoURL
	}
	if ref != "" {
		return repoURL + "@" + ref
	}
	return repoURL
}

// targetName returns the binary (and cache-directory) name for a target: the
// last segment of a /tree/ subdirectory, or the repository name otherwise.
func targetName(repoURL, subPath string) string {
	if subPath != "" {
		return lastSegment(subPath)
	}
	return repoName(repoURL)
}

func lastSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// semverTagPattern matches a semver tag with an optional leading "v".
var semverTagPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type semver struct {
	major, minor, patch int
	pre                 string
}

// less reports whether a is a lower version than b; a release sorts above its
// pre-releases.
func (a semver) less(b semver) bool {
	if a.major != b.major {
		return a.major < b.major
	}
	if a.minor != b.minor {
		return a.minor < b.minor
	}
	if a.patch != b.patch {
		return a.patch < b.patch
	}
	if a.pre == "" {
		return false
	}
	if b.pre == "" {
		return true
	}
	return a.pre < b.pre
}

func parseSemverTag(tag string) (semver, bool) {
	m := semverTagPattern.FindStringSubmatch(tag)
	if m == nil {
		return semver{}, false
	}
	nums := make([]int, 3)
	for i, s := range []string{m[1], m[2], m[3]} {
		n, err := strconv.Atoi(s)
		if err != nil {
			return semver{}, false
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], pre: m[4]}, true
}

// highestSemver returns the highest release (non-prerelease) tag among tags;
// if no release exists, the highest pre-release. "" if no semver tags match.
func highestSemver(tags []string) string {
	release, prerelease := "", ""
	var rv, pv semver
	for _, tag := range tags {
		v, ok := parseSemverTag(tag)
		if !ok {
			continue
		}
		if v.pre == "" {
			if release == "" || rv.less(v) {
				release, rv = tag, v
			}
		} else if prerelease == "" || pv.less(v) {
			prerelease, pv = tag, v
		}
	}
	if release != "" {
		return release
	}
	return prerelease
}

// parseLsRemote parses `git ls-remote --symref --tags <url>` output into
// non-peeled tag names and the default branch.
func parseLsRemote(out []byte) (tags []string, defaultBranch string) {
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		switch {
		case parts[1] == "HEAD" && strings.HasPrefix(parts[0], "ref: "):
			refname := strings.TrimPrefix(parts[0], "ref: ")
			if strings.HasPrefix(refname, "refs/heads/") {
				defaultBranch = strings.TrimPrefix(refname, "refs/heads/")
			}
		case strings.HasPrefix(parts[1], "refs/tags/") && !strings.HasSuffix(parts[1], "^{}"):
			tags = append(tags, strings.TrimPrefix(parts[1], "refs/tags/"))
		}
	}
	return tags, defaultBranch
}

// resolveLatestRef resolves "@latest" to a concrete ref and prints the mapping.
func resolveLatestRef(url, repoURL string) (string, error) {
	resolved, err := resolveLatest(repoURL)
	if err != nil {
		return "", fmt.Errorf("resolving @latest: %w", err)
	}
	fmt.Printf("latest: %s resolves to %s\n", url, resolved)
	return resolved, nil
}

// resolveLatest resolves "@latest" for a remote repo: the highest semver tag
// if any exist, otherwise the default branch.
func resolveLatest(url string) (string, error) {
	out, err := exec.Command("git", "ls-remote", "--symref", url).Output()
	if err != nil {
		return "", err
	}
	tags, defaultBranch := parseLsRemote(out)
	if best := highestSemver(tags); best != "" {
		return best, nil
	}
	if defaultBranch != "" {
		return defaultBranch, nil
	}
	return "", errors.New("no semver tags or default branch found")
}

func repoName(url string) string {
	url, _ = parseRef(url)
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")
	idx := strings.LastIndex(url, "/")
	if idx >= 0 {
		url = url[idx+1:]
	}
	return url
}

func repoNameFromDir(srcDir string) string {
	// best-effort: use the git remote to derive the binary name
	out, err := exec.Command("git", "-C", srcDir, "remote", "get-url", "origin").Output()
	if err == nil {
		if name := repoName(strings.TrimSpace(string(out))); name != "" {
			return name
		}
	}
	return filepath.Base(srcDir)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gorun: "+format+"\n", args...)
	os.Exit(1)
}
