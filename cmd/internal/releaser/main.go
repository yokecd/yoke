package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/yokecd/yoke/internal/x"
	"golang.org/x/mod/semver"

	"github.com/davidmdm/x/xerr"
)

func init() {
	dockerToken := os.Getenv("DOCKER_TOKEN")
	if dockerToken == "" {
		return
	}
	if err := x.X("docker login -u davidmdm -p " + dockerToken); err != nil {
		panic(fmt.Errorf("failed to login to docker: %w", err))
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
	}

	var errs []error
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
	Versions map[string]string
	Repo     *git.Repository
	DryRun   bool
}

func (releaser Releaser) ReleaseWasmBinary(name string) (err error) {
	version := releaser.Versions[name]

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

	nextVersion := func() string {
		patch := strings.TrimPrefix(semver.Canonical(version), semver.MajorMinor(version)+".")
		patchNum, _ := strconv.Atoi(patch)
		patchNum++
		return fmt.Sprintf("v0.0.%d", patchNum)
	}()

	fmt.Println("attempting to create release for version:", nextVersion)
	fmt.Println("building assets...")

	outputPath, err := build(filepath.Join("cmd", name))
	if err != nil {
		return fmt.Errorf("failed to build wasm: %w", err)
	}

	outputPath, err = compress(outputPath)
	if err != nil {
		return fmt.Errorf("failed to compress wasm: %w", err)
	}

	tag := path.Join(name, version)

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
	version := releaser.Versions[name]

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

	nextVersion := func() string {
		patch := strings.TrimPrefix(semver.Canonical(version), semver.MajorMinor(version)+".")
		patchNum, _ := strconv.Atoi(patch)
		patchNum++
		return fmt.Sprintf("v0.0.%d", patchNum)
	}()

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
		"docker buildx build -f ./Dockerfile.%s --platform linux/amd64,linux/arm64 -t davidmdm/%s:latest -t davidmdm/%s:%s --push .",
		[]any{name, name, name, strings.TrimPrefix(version, "v")},
	); err != nil {
		return fmt.Errorf("failed to build and push docker image: %w", err)
	}

	return nil
}

func (releaser Releaser) HasDiff(name, version string) (bool, error) {
	if version == "" {
		return true, nil
	}

	tag := path.Join(name, version)

	tagHash, err := releaser.Repo.ResolveRevision(plumbing.Revision(plumbing.NewTagReferenceName(tag)))
	if err != nil {
		return false, fmt.Errorf("failed to resolve: %s: %w", tag, err)
	}

	mainHash, err := releaser.Repo.ResolveRevision(plumbing.Revision(plumbing.NewBranchReferenceName("main")))
	if err != nil {
		return false, fmt.Errorf("failed to resolve: %s: %w", tag, err)
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
		headBinPath = filepath.Join(os.TempDir(), "head", name+".out")
	)

	if err := x.X(fmt.Sprintf("go build -o %s ./cmd/%s", tagBinPath, name)); err != nil {
		return false, fmt.Errorf("failed to build previous binary: %w", err)
	}

	if err := wt.Checkout(&git.CheckoutOptions{Hash: *mainHash}); err != nil {
		return false, fmt.Errorf("failed to checkout head: %w", err)
	}

	if err := x.X(fmt.Sprintf("go build -o %s ./cmd/%s", headBinPath, name)); err != nil {
		return false, fmt.Errorf("failed to build previous binary: %w", err)
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

func build(path string) (string, error) {
	_, name := filepath.Split(path)

	out := name + ".wasm"

	cmd := exec.Command("go", "build", "-o", out, "./"+path)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, output)
	}
	return out, nil
}

func compress(path string) (out string, err error) {
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

func getTagVersions(repo *git.Repository) (map[string]string, error) {
	iter, err := repo.Tags()
	if err != nil {
		return nil, fmt.Errorf("failed to read tags: %w", err)
	}

	versions := map[string]string{}

	iter.ForEach(func(r *plumbing.Reference) error {
		release, version := path.Split(r.Name()[len("refs/tags/"):].String())
		if !semver.IsValid(version) {
			return nil
		}
		release = path.Clean(release)
		if semver.Compare(version, versions[release]) > 0 {
			versions[release] = version
		}
		return nil
	})

	return versions, nil
}
