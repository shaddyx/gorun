package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

The first positional argument is the git URL; everything after it is
forwarded verbatim to the application.
`

func main() {
	upgrade := flag.Bool("upgrade", false, "force re-fetch and rebuild, then run")
	upgradeAll := flag.Bool("upgrade-all", false, "re-query git and rebuild every cached project")
	clean := flag.Bool("clean", false, "wipe the entire gorun cache")
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
		if err := upgradeAllCached(cacheRoot); err != nil {
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

	if err := run(cacheRoot, url, appArgs, *upgrade); err != nil {
		fatal("%v", err)
	}
}

func run(cacheRoot, url string, appArgs []string, upgrade bool) error {
	key := cacheKey(url)
	dir := filepath.Join(cacheRoot, key)
	srcDir := filepath.Join(dir, "src")
	binDir := filepath.Join(dir, "bin")
	name := repoName(url)
	binPath := filepath.Join(binDir, name)

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		if err := gitClone(url, srcDir); err != nil {
			return err
		}
	} else if upgrade {
		if err := gitPull(srcDir); err != nil {
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
		if err := goBuild(srcDir, binPath); err != nil {
			return err
		}
	}

	return execApp(binPath, appArgs)
}

func upgradeAllCached(cacheRoot string) error {
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

		name := repoNameFromDir(srcDir)
		binPath := filepath.Join(binDir, name)

		fmt.Printf("upgrading %s\n", e.Name())
		if err := gitPull(srcDir); err != nil {
			return err
		}
		if err := goBuild(srcDir, binPath); err != nil {
			return err
		}
	}
	return nil
}

func gitClone(url, srcDir string) error {
	fmt.Printf("cloning %s\n", url)
	cmd := exec.Command("git", "clone", "--depth", "1", url, srcDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitPull(srcDir string) error {
	fmt.Printf("pulling %s\n", srcDir)
	cmd := exec.Command("git", "-C", srcDir, "pull", "--ff-only")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func goBuild(srcDir, binPath string) error {
	fmt.Printf("building %s\n", binPath)
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

func repoName(url string) string {
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
