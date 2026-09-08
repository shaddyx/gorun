package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
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

The first positional argument is the git URL; everything after it is
forwarded verbatim to the application.

By default the main package is auto-detected: a repo whose binary lives in a
subdirectory (e.g. cmd/<name>) is located and built automatically. If several
main packages exist, or to pin a specific one, pass --main <dir>:
  gorun --main cmd/lazy-skill-mcp github.com/shaddyx/lazy-skills-mcp

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
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	if *clean && *upgradeAll {
		fatal("--clean and --upgrade-all are mutually exclusive")
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

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	url := args[0]
	appArgs := args[1:]

	if err := run(cacheRoot, url, appArgs, *upgrade, *verbose, *mainDir); err != nil {
		fatal("%v", err)
	}
}

func run(cacheRoot, url string, appArgs []string, upgrade, verbose bool, mainDir string) error {
	repoURL, ref := parseRef(url)
	repoURL = normalizeURL(repoURL)

	// The cache key uses the literal ref ("latest" stays "latest"), so @latest
	// always maps to the same stable cache dir regardless of the resolved version.
	keySource := repoURL
	if ref != "" {
		keySource = repoURL + "@" + ref
	}
	key := cacheKey(keySource)
	name := repoName(url)
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
		pkg, err := pickMainPackage(srcDir, mainDir, persisted)
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
		if b, err := os.ReadFile(filepath.Join(dir, "url")); err == nil {
			repoURL, r := parseRef(strings.TrimSpace(string(b)))
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

		name := repoNameFromDir(srcDir)
		binPath := filepath.Join(binDir, name)

		fmt.Printf("upgrading %s\n", e.Name())
		if err := gitPull(srcDir, ref, verbose); err != nil {
			return err
		}
		persisted := readBuildPath(dir)
		pkg, perr := pickMainPackage(srcDir, "", persisted)
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
	if target == "" {
		target = "origin/HEAD"
	}
	cmd = exec.Command("git", "-C", srcDir, "reset", "--hard", target)
	return runGit(cmd, verbose)
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
	target := "."
	if pkg != "" {
		target = "./" + pkg
	}
	fmt.Printf("building %s\n", binPath)
	cmd := exec.Command("go", "build", "-o", binPath, target)
	cmd.Dir = srcDir
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
		return "", fmt.Errorf("--main path %q is not a directory", rel)
	}
	if !isMainPackageDir(p) {
		return "", fmt.Errorf("--main path %q does not contain a main package", rel)
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
