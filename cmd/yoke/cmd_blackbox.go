package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"gopkg.in/yaml.v3"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/text"
)

type BlackboxParams struct {
	GlobalSettings
	Release        string
	Namespace      string
	RevisionID     int
	DiffRevisionID int
	Context        int
}

//go:embed cmd_blackbox_help.txt
var blackboxHelp string

func init() {
	blackboxHelp = strings.TrimSpace(internal.Colorize(blackboxHelp))
}

func GetBlackBoxParams(settings GlobalSettings, args []string) (*BlackboxParams, error) {
	flagset := flag.NewFlagSet("blackbox", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), blackboxHelp)
		flagset.PrintDefaults()
	}

	params := BlackboxParams{GlobalSettings: settings}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)
	flagset.IntVar(&params.Context, "context", 4, "number of lines of context in diff (ignored if not comparing revisions)")
	flagset.StringVar(&params.Namespace, "namespace", "", "namespace of release to inspect")
	flagset.Parse(args)

	params.Release = flagset.Arg(0)

	if revision := flagset.Arg(1); revision != "" {
		revisionID, err := strconv.Atoi(flagset.Arg(1))
		if err != nil {
			return nil, fmt.Errorf("revision must be an integer ID: %w", err)
		}
		params.RevisionID = revisionID
	}

	if revision := flagset.Arg(2); revision != "" {
		revisionID, err := strconv.Atoi(flagset.Arg(2))
		if err != nil {
			return nil, fmt.Errorf("revision to diff must be an integer ID: %w", err)
		}
		params.DiffRevisionID = revisionID
	}

	return &params, nil
}

func Blackbox(ctx context.Context, params BlackboxParams) error {
	client, err := k8s.NewClientFromConfigFlags(params.Kube)
	if err != nil {
		return fmt.Errorf("failed to instantiate k8 client: %w", err)
	}

	releases, err := func() ([]internal.Release, error) {
		if ns := params.Namespace; ns != "" {
			return client.GetReleasesByNS(ctx, ns)
		}
		return client.GetReleases(ctx)
	}()
	if err != nil {
		return fmt.Errorf("failed to get revisions: %w", err)
	}

	if params.Release == "" {
		tbl := table.NewWriter()
		tbl.SetStyle(table.StyleRounded)

		tbl.AppendHeader(table.Row{"release", "namespace", "revision id"})
		for _, release := range releases {
			tbl.AppendRow(table.Row{release.Name, release.Namespace, release.ActiveIndex() + 1})
		}

		_, err = io.WriteString(os.Stdout, tbl.Render()+"\n")
		return err
	}

	matchingReleases := internal.FindAll(releases, func(release internal.Release) bool {
		return release.Name == params.Release
	})
	if len(matchingReleases) == 0 {
		return fmt.Errorf("release %q not found", params.Release)
	}
	if len(matchingReleases) > 1 {
		return fmt.Errorf("release %q found in more than one namespace: specify namespace", params.Release)
	}

	release := matchingReleases[0]

	if params.RevisionID == 0 {
		tbl := table.NewWriter()
		tbl.SetStyle(table.StyleRounded)

		history := release.History
		slices.Reverse(history)

		tbl.AppendHeader(table.Row{"id", "resources", "flight", "sha", "created at"})
		for i, version := range history {
			tbl.AppendRow(table.Row{len(history) - i, version.Resources, version.Source.Ref, version.Source.Checksum, version.CreatedAt})
		}

		_, err = io.WriteString(os.Stdout, tbl.Render()+"\n")
		return err
	}

	if params.RevisionID > len(release.History) {
		return fmt.Errorf("revision %d not found", params.RevisionID)
	}

	stages, err := client.GetRevisionResources(ctx, release.History[params.RevisionID-1])
	if err != nil {
		return fmt.Errorf("failed to get resources for revision %d: %w", params.RevisionID, err)
	}

	primaryRevision := internal.CanonicalObjectMap(stages.Flatten())

	if params.DiffRevisionID == 0 {
		encoder := yaml.NewEncoder(os.Stdout)
		encoder.SetIndent(2)

		if err := encoder.Encode(primaryRevision); err != nil {
			return fmt.Errorf("failed to encode resources: %w", err)
		}
		return nil
	}

	if params.DiffRevisionID > len(release.History) {
		return fmt.Errorf("revision %d not found", params.DiffRevisionID)
	}

	stages, err = client.GetRevisionResources(ctx, release.History[params.DiffRevisionID-1])
	if err != nil {
		return fmt.Errorf("failed to get resources for revision %d: %w", params.DiffRevisionID, err)
	}

	diffRevision := internal.CanonicalObjectMap(stages.Flatten())

	a, err := text.ToYamlFile(fmt.Sprintf("revision %d", params.RevisionID), primaryRevision)
	if err != nil {
		return err
	}

	b, err := text.ToYamlFile(fmt.Sprintf("revision %d", params.DiffRevisionID), diffRevision)
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(internal.Stdout(ctx), text.DiffColorized(a, b, params.Context))
	return err
}
