package main

import (
	"cmp"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	y3 "gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/davidmdm/x/xerr"
	"github.com/davidmdm/yoke/internal"
	"github.com/davidmdm/yoke/internal/k8s"
	"github.com/davidmdm/yoke/internal/text"
	"github.com/davidmdm/yoke/internal/wasi"
	"github.com/davidmdm/yoke/pkg/yoke"
)

type TakeoffFlightParams struct {
	Path      string
	Input     io.Reader
	Args      []string
	Namespace string
}

type TakeoffParams struct {
	GlobalSettings
	TestRun        bool
	SkipDryRun     bool
	ForceConflicts bool
	Release        string
	Out            string
	Flight         TakeoffFlightParams
	DiffOnly       bool
	Context        int
}

//go:embed cmd_takeoff_help.txt
var takeoffHelp string

func init() {
	takeoffHelp = strings.TrimSpace(internal.Colorize(takeoffHelp))
}

func GetTakeoffParams(settings GlobalSettings, source io.Reader, args []string) (*TakeoffParams, error) {
	flagset := flag.NewFlagSet("takeoff", flag.ExitOnError)

	flagset.Usage = func() {
		fmt.Fprintln(flagset.Output(), takeoffHelp)
		flagset.PrintDefaults()
	}

	params := TakeoffParams{
		GlobalSettings: settings,
		Flight:         TakeoffFlightParams{Input: source},
	}

	RegisterGlobalFlags(flagset, &params.GlobalSettings)

	flagset.BoolVar(&params.TestRun, "test-run", false, "test-run executes the underlying wasm and outputs it to stdout but does not apply any resources to the cluster")
	flagset.BoolVar(&params.SkipDryRun, "skip-dry-run", false, "disables running dry run to resources before applying them")
	flagset.BoolVar(&params.ForceConflicts, "force-conflicts", false, "force apply changes on field manager conflicts")
	flagset.BoolVar(&params.DiffOnly, "diff-only", false, "show diff between current revision and would be applied state. Does not apply anything to cluster")
	flagset.IntVar(&params.Context, "context", 4, "number of lines of context in diff (ignored if not using --diff-only)")
	flagset.StringVar(&params.Out, "out", "", "if present outputs flight resources to directory specified, if out is - outputs to standard out")
	flagset.StringVar(&params.Flight.Namespace, "namespace", "default", "preferred namespace for resources if they do not define one")

	args, params.Flight.Args = internal.CutArgs(args)

	flagset.Parse(args)

	params.Release = flagset.Arg(0)
	params.Flight.Path = flagset.Arg(1)

	if params.Release == "" {
		return nil, fmt.Errorf("release is required as first positional arg")
	}
	if params.Flight.Input == nil && params.Flight.Path == "" {
		return nil, fmt.Errorf("flight-path is required as second position arg")
	}

	return &params, nil
}

func TakeOff(ctx context.Context, params TakeoffParams) error {
	output, wasm, err := EvalFlight(ctx, params.Release, params.Flight)
	if err != nil {
		return fmt.Errorf("failed to evaluate flight: %w", err)
	}
	if params.TestRun {
		_, err = fmt.Print(string(output))
		return err
	}

	kube, err := k8s.NewClientFromKubeConfig(params.KubeConfigPath)
	if err != nil {
		return err
	}

	client := yoke.FromK8Client(kube)

	var resources internal.List[*unstructured.Unstructured]
	if err := yaml.Unmarshal(output, &resources); err != nil {
		return fmt.Errorf("failed to unmarshal raw resources: %w", err)
	}

	complete := internal.DebugTimer(ctx, "looking up resource mappings")

	for _, resource := range resources {
		mapping, err := kube.LookupResourceMapping(resource)
		if err != nil {
			return err
		}
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace && resource.GetNamespace() == "" {
			resource.SetNamespace(cmp.Or(params.Flight.Namespace, "default"))
		}
	}

	complete()

	internal.AddHallmouiMetadata(resources, params.Release)

	if params.Out != "" {
		if params.Out == "-" {
			return ExportToStdout(resources)
		}
		return ExportToFS(params.Out, params.Release, resources)
	}

	if params.DiffOnly {
		revisions, err := kube.GetRevisions(ctx, params.Release)
		if err != nil {
			return fmt.Errorf("failed to get revision history: %w", err)
		}
		currentResources := revisions.CurrentResources()

		a, err := text.ToYamlFile("current", currentResources)
		if err != nil {
			return err
		}

		b, err := text.ToYamlFile("next", resources)
		if err != nil {
			return err
		}

		_, err = fmt.Fprint(os.Stdout, text.DiffColorized(a, b, params.Context))
		return err
	}

	return client.Takeoff(ctx, yoke.TakeoffParams{
		Release:        params.Release,
		Resources:      resources,
		FlightID:       params.Flight.Path,
		Namespace:      params.Flight.Namespace,
		Wasm:           wasm,
		SkipDryRun:     params.SkipDryRun,
		ForceConflicts: params.ForceConflicts,
	})
}

func ExportToFS(dir, release string, resources []*unstructured.Unstructured) error {
	root := filepath.Join(dir, release)

	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("failed remove previous flight export: %w", err)
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("failed to create release output directory: %w", err)
	}

	var errs []error
	for _, resource := range resources {
		path := filepath.Join(root, internal.Canonical(resource)+".yaml")

		if err := internal.WriteYAML(path, resource.Object); err != nil {
			errs = append(errs, err)
		}
	}

	return xerr.MultiErrFrom("failed to write resource(s)", errs...)
}

func ExportToStdout(resources []*unstructured.Unstructured) error {
	output := make(map[string]any, len(resources))
	for _, resource := range resources {
		output[internal.Canonical(resource)] = resource.Object
	}

	encoder := y3.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	return encoder.Encode(output)
}

func EvalFlight(ctx context.Context, release string, flight TakeoffFlightParams) ([]byte, []byte, error) {
	if flight.Input != nil && flight.Path == "" {
		output, err := io.ReadAll(flight.Input)
		return output, nil, err
	}

	wasm, err := yoke.LoadWasm(ctx, flight.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read wasm program: %w", err)
	}

	output, err := wasi.Execute(ctx, wasi.ExecParams{
		Wasm:    wasm,
		Release: release,
		Stdin:   flight.Input,
		Args:    flight.Args,
		Env:     map[string]string{"NAMESPACE": flight.Namespace},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute wasm: %w", err)
	}

	return output, wasm, nil
}
