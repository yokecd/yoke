package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"syscall"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/davidmdm/x/xcontext"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/yoke"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	ctx, cancel := xcontext.WithSignalCancelation(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	opts := providerserver.ServeOpts{
		// TODO: Update this string with the published name of your provider.
		// Also update the tfplugindocs generate command to either remove the
		// -provider-name flag or set its value to the updated provider name.
		Address: "registry.terraform.io/yokecd/yoke",
		Debug:   debug,
	}

	return providerserver.Serve(ctx, CreateProvider, opts)
}

func CreateProvider() provider.Provider {
	return new(Provider)
}

var _ provider.Provider = (*Provider)(nil)

type KubernetesConfig struct {
	KubePath    string `tfsdk:"config_path"`
	KubeContext string `tfsdk:"config_context"`
}

type ProviderConfig struct {
	Kubernetes KubernetesConfig
}

type Provider struct{}

// Configure implements [provider.Provider].
func (p *Provider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg ProviderConfig
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)

	kubeflags := genericclioptions.NewConfigFlags(false)
	kubeflags.KubeConfig = &cfg.Kubernetes.KubePath
	kubeflags.Context = &cfg.Kubernetes.KubeContext

	commander, err := yoke.FromKubeConfigFlags(kubeflags)
	if err != nil {
		resp.Diagnostics.AddError("failed to initialize yoke client", err.Error())
	}
	resp.ResourceData = commander
}

// DataSources implements [provider.Provider].
func (p *Provider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

// Metadata implements [provider.Provider].
func (p *Provider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "yoke"
	resp.Version = internal.GetYokeVersion()
}

// Resources implements [provider.Provider].
func (p *Provider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return new(Release) },
	}
}

// Schema implements [provider.Provider].
func (p *Provider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = providerschema.Schema{
		Attributes: map[string]providerschema.Attribute{
			"kubernetes": providerschema.MapNestedAttribute{
				Required: true,
				NestedObject: providerschema.NestedAttributeObject{
					Attributes: map[string]providerschema.Attribute{
						"config_path":    providerschema.StringAttribute{Required: true},
						"config_context": providerschema.StringAttribute{Optional: true},
					},
				},
			},
		},
		Description:         "",
		MarkdownDescription: "",
		DeprecationMessage:  "",
	}
}

type Release struct {
	commander *yoke.Commander
}

var _ resource.Resource = (*Release)(nil)

// Create implements [resource.Resource].
func (r *Release) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	panic("unimplemented")
}

// Delete implements [resource.Resource].
func (r *Release) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	panic("unimplemented")
}

// Metadata implements [resource.Resource].
func (r *Release) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_release"
}

// Read implements [resource.Resource].
func (r *Release) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	panic("unimplemented")
}

// Schema implements [resource.Resource].
func (r *Release) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Attributes: map[string]resourceschema.Attribute{
			"wasm_url": resourceschema.StringAttribute{Required: true},
			"input":    resourceschema.StringAttribute{Optional: true},
			"args": resourceschema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},
			"cluster_access": resourceschema.BoolAttribute{Optional: true},
			"resource_access_matchers": resourceschema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
			},
			"max_memory_mib": resourceschema.Int32Attribute{Required: false},
			// TODO: this needs to map onto time.Duration... Will figure that out later.
			"timeout": resourceschema.StringAttribute{Required: false},
			"prune": resourceschema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]resourceschema.Attribute{
					"crds":       resourceschema.BoolAttribute{Optional: true},
					"namespaces": resourceschema.BoolAttribute{Optional: true},
				},
			},
		},
	}
}

// Update implements [resource.Resource].
func (r *Release) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	panic("unimplemented")
}

func (r *Release) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		resp.Diagnostics.AddError("unable to configure release", "provider data not present for release configuration")
		return
	}

	commander, ok := req.ProviderData.(*yoke.Commander)
	if !ok {
		resp.Diagnostics.AddError("unexpected resource configure type", fmt.Sprintf("expected *yoke.Commander but got %T", req.ProviderData))
		return
	}

	r.commander = commander
}
