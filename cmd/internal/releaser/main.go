package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"golang.org/x/mod/semver"

	"github.com/davidmdm/x/xerr"

	"github.com/yokecd/yoke/internal/x"
)

func init() {
	var (
		actor = os.Getenv("GITHUB_ACTOR")
		token = os.Getenv("GITHUB_TOKEN")
	)
	if token == "" || actor == "" {
		fmt.Println("skipping docker login to ghcr.io")
		return
	}
	if err := x.Xf("docker login ghcr.io -u %s --password-stin ", []any{actor}, x.Input(strings.NewReader(token))); err != nil {
		panic(fmt.Errorf("failed to login to ghcr.io: %w", err))
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dry := flag.Bool("dry", false, "dry-run")
	cli := flag.Bool("cli", false, "release main yoke cli")
	local := flag.Bool("local", false, "run agains the locally checkout version instead of main")

	var wasms []string
	flag.Func("wasm", "commands to buid as wasm and release", func(value string) error {
		wasms = append(wasms, value)
		return nil
	})

	var dockers []string
	flag.Func("docker", "dockerfiles to build and release", func(value string) error {
		dockers = append(dockers, value)
		return nil
	})

	flag.Parse()

	repo, err := git.PlainOpen(".")
	if err != nil {
		return fmt.Errorf("failed to open git repo: %w", err)
	}

	versions, err := getTagVersions(repo)
	if err != nil {
		return fmt.Errorf("failed to get repo's versions by tag: %w", err)
	}

	releaser := Releaser{
		Versions: versions,
		Repo:     repo,
		DryRun:   *dry,
		Local:    *local,
	}

	var errs []error

	if *cli {
		if err := releaser.ReleaseYokeCLI(); err != nil {
			errs = append(errs, fmt.Errorf("failed to release yoke cli: %w", err))
		}
	}

	for _, cmd := range wasms {
		if err := releaser.ReleaseWasmBinary(cmd); err != nil {
			errs = append(errs, fmt.Errorf("failed to release wasm binary: %s: %v", cmd, err))
		}
	}

	for _, docker := range dockers {
		if err := releaser.ReleaseDockerFile(docker); err != nil {
			errs = append(errs, fmt.Errorf("failed to release dockerfile: %s: %v", docker, err))
		}
	}

	return xerr.MultiErrOrderedFrom("", errs...)
}

type Releaser struct {
	Versions map[string]TagVersion
	Repo     *git.Repository
	DryRun   bool
	Local    bool
	Breaking bool
}

func (releaser Releaser) ReleaseYokeCLI() error {
	version := releaser.Versions["."].V
	hash := releaser.Versions["."].H

	diff, err := releaser.HasDiff("yoke", version)
	if err != nil {
		return fmt.Errorf("failed to check diff: %w", err)
	}
	if !diff {
		fmt.Println("skipping release: no diff found")
		return nil
	}

	breaking, err := containsBreakingChange(releaser.Repo, hash)
	if err != nil {
		return err
	}

	nextVersion := releaser.Bump(version, breaking)

	if releaser.DryRun {
		fmt.Println("dry-run: release yoke cli via tag:", nextVersion)
		return nil
	}

	targets := []struct {
		OS   string
		Arch []string
	}{
		{
			OS:   "darwin",
			Arch: []string{"arm64", "amd64"},
		},
		{
			OS:   "linux",
			Arch: []string{"arm64", "amd64", "arm", "386"},
		},
		{
			OS:   "windows",
			Arch: []string{"amd64"},
		},
	}

	var compressedPaths []string
	for _, target := range targets {
		for _, arch := range target.Arch {
			binaryPath := fmt.Sprintf("./build_output/yoke_%s_%s_%s", nextVersion, target.OS, arch)
			if target.OS == "windows" {
				binaryPath += ".exe"
			}

			env := x.Env("GOOS="+target.OS, "GOARCH="+arch)
			if err := x.Xf("go build -o %s ./cmd/yoke", []any{binaryPath}, env); err != nil {
				return fmt.Errorf("failed to build %s: %v", binaryPath, err)
			}

			compressedPath, err := compressFile(binaryPath)
			if err != nil {
				return fmt.Errorf("failed to compress %s: %w", binaryPath, err)
			}

			compressedPaths = append(compressedPaths, compressedPath)
		}
	}

	if err := x.Xf("gh release create %s %s", []any{nextVersion, strings.Join(compressedPaths, " ")}); err != nil {
		return fmt.Errorf("failed to create github release: %v", err)
	}

	return nil
}

func (releaser Releaser) ReleaseWasmBinary(name string) (err error) {
	version := releaser.Versions[name].V
	hash := releaser.Versions[name].H

	diff, err := releaser.HasDiff(name, version)
	if err != nil {
		return fmt.Errorf("failed to check for diff: %w", err)
	}

	if !diff {
		fmt.Printf("skipping release: no diff found for command %s with previous version: %s\n", name, version)
		return nil
	}

	if version == "" {
		fmt.Println("No version found for", name)
	} else if semver.Compare(version, "v0.0.1") < 0 {
		fmt.Printf("%s is pre v0.0.1... tagging new release\n", name)
	}

	breaking, err := containsBreakingChange(releaser.Repo, hash)
	if err != nil {
		return err
	}

	nextVersion := releaser.Bump(version, breaking)

	fmt.Println("attempting to create release for version:", nextVersion)
	fmt.Println("building assets...")

	outputPath, err := buildWasm(filepath.Join("cmd", name))
	if err != nil {
		return fmt.Errorf("failed to build wasm: %w", err)
	}

	outputPath, err = compressFile(outputPath)
	if err != nil {
		return fmt.Errorf("failed to compress wasm: %w", err)
	}

	tag := path.Join(name, nextVersion)

	if releaser.DryRun {
		fmt.Println("dry-run: create realease", tag)
		return nil
	}

	if err := x.Xf("gh release create %s %s", []any{tag, outputPath}); err != nil {
		return fmt.Errorf("failed to create github release: %w", err)
	}

	return nil
}

func (releaser Releaser) ReleaseDockerFile(name string) error {
	version := releaser.Versions[name].V
	hash := releaser.Versions[name].H

	diff, err := releaser.HasDiff(name, version)
	if err != nil {
		return fmt.Errorf("failed to check for diff againt version: %s: %v", version, err)
	}

	if !diff {
		fmt.Println("skipping release: no diff found against version:", version)
		return nil
	}

	if version == "" {
		fmt.Println("No version found for", name)
	} else if semver.Compare(version, "v0.0.1") < 0 {
		fmt.Printf("%s is pre v0.0.1... tagging new release\n", name)
	}

	breaking, err := containsBreakingChange(releaser.Repo, hash)
	if err != nil {
		return err
	}

	nextVersion := releaser.Bump(version, breaking)

	fmt.Println("attempting to create release for version:", nextVersion)
	fmt.Println("building assets...")

	if releaser.DryRun {
		fmt.Println("dry run: create buildx, and build&push docker image")
		return nil
	}

	if err := x.X("docker buildx create --use"); err != nil {
		return fmt.Errorf("failed to create docker builder: %w", err)
	}

	if err := x.Xf(
		"docker buildx build -f ./Dockerfile.%s --platform linux/amd64,linux/arm64 -t ghcr.io/yokecd/%s:latest -t ghcr.io/yokecd/%s:%s --push .",
		[]any{name, name, name, strings.TrimPrefix(nextVersion, "v")},
	); err != nil {
		return fmt.Errorf("failed to build and push docker image: %w", err)
	}

	tag := path.Join(name, nextVersion)

	if err := x.Xf("git tag %s", []any{tag}); err != nil {
		return fmt.Errorf("failed to tag repository: %w", err)
	}

	if err := x.X("git push --tags"); err != nil {
		return fmt.Errorf("failed to push tags")
	}

	return nil
}

func (releaser Releaser) HasDiff(name, version string) (bool, error) {
	if version == "" {
		return true, nil
	}

	tag := func() string {
		if name == "yoke" {
			return version
		}
		return path.Join(name, version)
	}()

	tagHash, err := releaser.Repo.ResolveRevision(plumbing.Revision(plumbing.NewTagReferenceName(tag)))
	if err != nil {
		return false, fmt.Errorf("failed to resolve: %s: %w", tag, err)
	}

	branchRef, err := func() (plumbing.ReferenceName, error) {
		if !releaser.Local {
			return plumbing.NewBranchReferenceName("main"), nil
		}

		head, err := releaser.Repo.Head()
		if err != nil {
			return "", fmt.Errorf("failed to get head: %w", err)
		}

		branches, err := releaser.Repo.Branches()
		if err != nil {
			return "", fmt.Errorf("failed to lookup branches: %w", err)
		}
		defer branches.Close()

		for {
			branch, err := branches.Next()
			if branch != nil && branch.Hash() == head.Hash() {
				return branch.Name(), nil
			}
			if err == io.EOF {
				return "", fmt.Errorf("failed to find local branch")
			}
		}
	}()
	if err != nil {
		return false, fmt.Errorf("failed to get target branch: %w", err)
	}

	wt, err := releaser.Repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("failed to get worktree: %w", err)
	}

	if err := wt.Checkout(&git.CheckoutOptions{Hash: *tagHash}); err != nil {
		return false, fmt.Errorf("failed to checkout %q: %w", tag, err)
	}

	var (
		tagBinPath  = filepath.Join(os.TempDir(), tag, name+".out")
		headBinPath = filepath.Join(os.TempDir(), branchRef.Short(), name+".out")
	)

	if err := withoutGit(func() error { return x.X(fmt.Sprintf("go build -o %s ./cmd/%s", tagBinPath, name)) }); err != nil {
		return false, fmt.Errorf("failed to build previous binary: %w", err)
	}

	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef}); err != nil {
		return false, fmt.Errorf("failed to checkout %s: %w", branchRef.Short(), err)
	}

	if err := withoutGit(func() error { return x.X(fmt.Sprintf("go build -o %s ./cmd/%s", headBinPath, name)) }); err != nil {
		return false, fmt.Errorf("failed to build next binary: %w", err)
	}

	tagData, err := os.ReadFile(tagBinPath)
	if err != nil {
		return false, fmt.Errorf("failed to read tag binary: %w", err)
	}

	headData, err := os.ReadFile(headBinPath)
	if err != nil {
		return false, fmt.Errorf("failed to read head binary path: %w", err)
	}

	return !reflect.DeepEqual(tagData, headData), nil
}

func (releaser Releaser) Bump(version string, breaking bool) string {
	if !breaking {
		return bumpPatch(version)
	}
	return bumpMinor(version)
}

func buildWasm(path string) (string, error) {
	_, name := filepath.Split(path)
	out := name + ".wasm"
	err := x.Xf("go build -o %s ./%s", []any{out, path}, x.Env("GOOS=wasip1", "GOARCH=wasm"))
	return out, err
}

func compressFile(path string) (out string, err error) {
	output := path + ".gz"

	destination, err := os.Create(output)
	if err != nil {
		return "", err
	}
	defer func() {
		err = xerr.MultiErrFrom("", err, destination.Close())
	}()

	compressor, err := gzip.NewWriterLevel(destination, gzip.BestCompression)
	if err != nil {
		return "", fmt.Errorf("could not create gzip writer: %w", err)
	}
	defer func() {
		err = xerr.MultiErrFrom("", err, compressor.Close())
	}()

	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		err = xerr.MultiErrFrom("", err, source.Close())
	}()

	if _, err := io.Copy(compressor, source); err != nil {
		return "", err
	}

	return output, nil
}

type TagVersion struct {
	V string
	H plumbing.Hash
}

func getTagVersions(repo *git.Repository) (map[string]TagVersion, error) {
	iter, err := repo.Tags()
	if err != nil {
		return nil, fmt.Errorf("failed to read tags: %w", err)
	}

	versions := map[string]TagVersion{}

	iter.ForEach(func(r *plumbing.Reference) error {
		release, version := path.Split(r.Name()[len("refs/tags/"):].String())
		if !semver.IsValid(version) {
			return nil
		}
		release = path.Clean(release)
		if semver.Compare(version, versions[release].V) > 0 {
			versions[release] = TagVersion{
				V: version,
				H: r.Hash(),
			}
		}
		return nil
	})

	return versions, nil
}

func bumpPatch(version string) string {
	if version == "" {
		return "v0.0.1"
	}
	canonical := semver.Canonical(version)
	majorMinor := semver.MajorMinor(version)
	patch := canonical[len(majorMinor)+1:]
	patchNumber, _ := strconv.Atoi(patch)
	return fmt.Sprintf("%s.%d", majorMinor, patchNumber+1)
}

func bumpMinor(version string) string {
	major := semver.Major(version)
	minor := semver.MajorMinor(version)[len(major)+1:]
	minorNum, _ := strconv.Atoi(minor)
	return fmt.Sprintf("%s.%d.0", major, minorNum+1)
}

func withoutGit(fn func() error) error {
	if err := x.X("mv .git .gitbak"); err != nil {
		panic(err)
	}

	defer func() {
		if err := x.X("mv .gitbak .git"); err != nil {
			panic(err)
		}
	}()

	return fn()
}

func containsBreakingChange(repo *git.Repository, since plumbing.Hash) (bool, error) {
	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err != nil {
		return false, fmt.Errorf("failed to git log: %w", err)
	}

	for {
		commit, err := iter.Next()
		if commit != nil && commit.Hash == since {
			break
		}
		if err == io.EOF {
			break
		}
		if strings.Contains(strings.ToLower(commit.Message), "breaking change:") {
			return true, nil
		}
	}

	return false, nil
}
