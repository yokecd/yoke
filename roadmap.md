# Roadmap

The following is a non-exhaustive list of features/changes that is on the roadmap for the yoke project.

Suggestions are welcome.

## 2026

- [ ] FluxCD Support for the ATC

> The ATC can already be used in conjunction with Flux or ArgoCD to use GitOps principles to apply Flights and Custom APIs managed by the ATC.
> However, although the Yoke project supports a first-class ArgoCD integration via the `yokecd` ArgoCD Config Management Plugin (CMP), it does not yet
> offer a tight integration into the Flux ecosystem.

- [ ] Flight and ClusterFlight APIs should support the same modes as Airways: standard, static, dynamic, and subscription.

> Custom APIs declared via Airways can choose a mode which allows you to control admission behavior or requeue the resource for evaluation
> when any sub-resources or subscribed-to external resources are updated. This is an important feature which allows flight authors to take advantage of
> the ATC's server-side approach and run their package logic as a reconciliation loop. This should be implemented for Flights/ClusterFlights.

- [x] (Breaking change :: minor) Airways should no longer have the `CrossNamespace` option. Instead, this takeoff option should be inferred from the Airway's child CRD scope.

> Cross-namespace flights only make sense when the parent resources are cluster-scoped; otherwise, the parent cannot own its children in all namespaces where they will be deployed.
> Hence, there is no reason to ask the Airway author to set this value explicitly. If the Airway is namespace-scoped, `CrossNamespace=false`, and if cluster-scoped, `CrossNamespace=true`.

- [ ] (Breaking change :: minor) Releases created by instances of Airway Custom Resources should use the Yoke resource reference format.

> Previously, Yoke releases were limited in character set and length as they needed to be a valid DNS subdomain. This limitation has since been removed.
> This has allowed flights to be more appropriately represented using Yoke's internal resource reference representation: `namespace/group.kind:name`.
> Currently, Airway instances use `group.kind.namespace?.name`. This is ambiguous as it is difficult to determine which segments belong to the group, and you need to know the
> scoping of the resource to know if the namespace is present at all. Fixing this would create an unambiguous reference and a more unified user experience.
> Ideally this should be done in a backward compatible way, migrating the old-format to the new as the ATC redeploys.


- [x] Feature: Airway instance status updates without modifying the release state.

> Currently, on every Airway instance evaluation, the custom resource is passed to the flight implementation and the release state is calculated. In dynamic and subscription modes,
> certain events will retrigger evaluation, allowing authors to build the state progressively via orchestration (e.g., waiting for a secret to be created before creating a deployment).
> However, on errors, authors may want to update the status of the instance without returning the desired state of the release.
> Currently, this requires rebuilding the entire current state, which can be error-prone and potentially destructive.
> To this end, if a flight returns only its parent resource, the ATC should update the status without modifying the release state.

- [ ] Feature: Adding events to Flights, Airways, and Airway Instances.

> Currently the only information to be found about the state of a Flight, Airway, or Airway instance is in the status of the resource.
> This means we leave a lot of historical data on the table, such as how often these objects are being reconciled, or simply the order of operations
> that led to the current state. Using the K8s events API will increase UX overall.

- [ ] Feature: atc&yokecd installers: allow users to configure annotations on the service account and deployment resources to enable WorkloadIdentityFederation.

> Currently the ATC and YokeCD Plugin Server supports private registries via OCI. The mechanism is that admins must configure a dockerconfig secret reference to be used by the ATC.
> However in many Cloud production workloads, users will want to use WorkloadIdentityFederation. Depending on the cloud-vendor, they will need to add annotations/labels
> to the service-account, deployment, or both.

- [ ] Feature: OCI mTLS support

> Some users self-host their own OCI registries. Often one of the most secure ways to expose these registries is via mTLS. Airways and Flights should expose an option
> to allow users to point to secrets referencing mTLS attributes tls.crt, tls.key, ca.crt.

- [ ] Flight Lookups should not panic outside of wasip1 environments

> Currently the wasi sdk for yoke exposes lookup functions to read cluster state. However it uses build tags to use the wasi implementation only when the OS is wasip1.
> For all other OS's it panics. However this makes it harder to preview the result of such code locally via the language's native toolchain (in this case Go). To test you have no choice,
> but to compile to wasm and use yoke in dry mode or with `-stdout`. Implementing the lookup interface outside of wasip1 will allow easier access to native testing.
