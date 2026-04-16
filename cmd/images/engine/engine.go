package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
)

type Engine struct {
	*dagger.Client
	Regsitry string
}

func NewEngine(ctx context.Context, registry string) (*Engine, error) {
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to dagger engine: %w", err)
	}
	return &Engine{Client: client, Regsitry: registry}, nil
}

func (engine Engine) Build(ctx context.Context, command string, tags []string) error {
	fmt.Fprintln(os.Stderr, "building")
	platform, err := engine.DefaultPlatform(ctx)
	if err != nil {
		return fmt.Errorf("failed to get default platform: %w", err)
	}
	containers := engine.buildContainers([]dagger.Platform{platform}, command)
	for _, tag := range tags {
		ref := fmt.Sprintf("yokecd/%s:%s", command, tag)
		if err := containers[0].ExportImage(ctx, ref); err != nil {
			return fmt.Errorf("failed to export: %s: %w", ref, err)
		}
	}
	return nil
}

const (
	linuxArm64 dagger.Platform = "linux/arm64"
	linuxAmd64 dagger.Platform = "linux/amd64"
)

func (engine Engine) Publish(ctx context.Context, command string, tags []string) error {
	fmt.Fprintln(os.Stderr, "publishing")
	containers := engine.buildContainers([]dagger.Platform{linuxAmd64, linuxArm64}, command)
	for _, tag := range tags {
		address := fmt.Sprintf("%s/yokecd/%s:%s", engine.Regsitry, command, tag)
		if _, err := engine.Container().Publish(ctx, address, dagger.ContainerPublishOpts{PlatformVariants: containers}); err != nil {
			return fmt.Errorf("failed to publish: %s: %w", address, err)
		}
	}
	return nil
}

func (engine Engine) buildContainers(platforms []dagger.Platform, command string) []*dagger.Container {
	base := engine.Container(dagger.ContainerOpts{}).
		From("golang:1.26-alpine").
		WithWorkdir("/app").
		WithFile("go.mod", engine.Host().File("go.mod")).
		WithFile("go.sum", engine.Host().File("go.sum")).
		WithExec([]string{"go", "mod", "download"})

	for _, mount := range []string{"cmd", "internal", "pkg"} {
		base = base.WithDirectory(mount, engine.Host().Directory(mount, dagger.HostDirectoryOpts{Gitignore: true}))
	}

	var (
		variants []*dagger.Container
		source   = "./cmd/" + command
		output   = "./" + command
	)
	for _, platform := range platforms {
		os, arch, _ := strings.Cut(string(platform), "/")

		binary := base.
			WithEnvVariable("GOOS", os).
			WithEnvVariable("GOARCH", arch).
			WithExec([]string{"go", "build", "-o", output, source}).
			File(output)

		variants = append(
			variants,
			engine.
				Container(dagger.ContainerOpts{Platform: platform}).
				From("alpine:3.21").
				WithWorkdir("app").
				WithFile("./atc", binary).
				WithEntrypoint([]string{"./atc"}),
		)
	}

	return variants
}
