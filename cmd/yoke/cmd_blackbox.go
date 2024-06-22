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
	"k8s.io/client-go/tools/clientcmd"

	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/internal/k8s"
	"github.com/davidmdm/yoke/internal/text"
)

type BlackboxParams struct {
	GlobalSettings
	Release          string
	ResourceMappings bool
	RevisionID       int
	DiffRevisionID   int
	Context          int
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
	flagset.BoolVar(&params.ResourceMappings, "mapping", false, "print release to resource mappings. If present ignores all other args")
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
	restcfg, err := clientcmd.BuildConfigFromFlags("", params.KubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to build k8 config: %w", err)
	}

	client, err := k8s.NewClient(restcfg)
	if err != nil {
		return fmt.Errorf("failed to instantiate k8 client: %w", err)
	}

	if params.ResourceMappings {
		mappings, err := client.GetResourceReleaseMapping(ctx)
		if err != nil {
			return fmt.Errorf("failed to lookup resource to release mappings: %w", err)
		}

		relToRes := make(map[string][]string)
		for resource, release := range mappings {
			relToRes[release] = append(relToRes[release], resource)
		}

		encoder := yaml.NewEncoder(os.Stdout)
		encoder.SetIndent(2)
		return encoder.Encode(relToRes)
	}

	allReleases, err := client.GetAllRevisions(ctx)
	if err != nil {
		return fmt.Errorf("failed to get revisions: %w", err)
	}
	if params.Release == "" {
		tbl := table.NewWriter()
		tbl.SetStyle(table.StyleRounded)

		tbl.AppendHeader(table.Row{"release", "revision id"})
		for _, revisions := range allReleases {
			tbl.AppendRow(table.Row{revisions.Release, revisions.ActiveIndex() + 1})
		}

		_, err = io.WriteString(os.Stdout, tbl.Render()+"\n")
		return err
	}

	revisions, ok := internal.Find(allReleases, func(revisions internal.Revisions) bool {
		return revisions.Release == params.Release
	})
	if !ok {
		return fmt.Errorf("release %q not found", params.Release)
	}

	if params.RevisionID == 0 {
		tbl := table.NewWriter()
		tbl.SetStyle(table.StyleRounded)

		history := revisions.History
		slices.Reverse(history)

		tbl.AppendHeader(table.Row{"id", "resources", "flight", "sha", "created at"})
		for i, version := range history {
			tbl.AppendRow(table.Row{len(history) - i, version.Resources, version.Source.Ref, version.Source.Checksum, version.CreatedAt})
		}

		_, err = io.WriteString(os.Stdout, tbl.Render()+"\n")
		return err
	}

	if params.RevisionID > len(revisions.History) {
		return fmt.Errorf("revision %d not found", params.RevisionID)
	}

	resources, err := client.GetRevisionResources(ctx, revisions.History[params.RevisionID-1])
	if err != nil {
		return fmt.Errorf("failed to get resources for revision %d: %w", params.RevisionID, err)
	}

	primaryRevision := internal.CanonicalObjectMap(resources)

	if params.DiffRevisionID == 0 {
		encoder := yaml.NewEncoder(os.Stdout)
		encoder.SetIndent(2)

		if err := encoder.Encode(primaryRevision); err != nil {
			return fmt.Errorf("failed to encode resources: %w", err)
		}
		return nil
	}

	if params.DiffRevisionID > len(revisions.History) {
		return fmt.Errorf("revision %d not found", params.DiffRevisionID)
	}

	resources, err = client.GetRevisionResources(ctx, revisions.History[params.DiffRevisionID-1])
	if err != nil {
		return fmt.Errorf("failed to get resources for revision %d: %w", params.DiffRevisionID, err)
	}

	diffRevision := internal.CanonicalObjectMap(resources)

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
