# yoke - Infrastructure-as-Code (IaC) Package Deployer for Kubernetes

<img src="./halloumi.jpg" width=600/>

## Overview

yoke is a Helm-inspired infrastructure-as-code (IaC) package deployer.

The philosophy behind yoke is that Kubernetes packages should be described via code. Programming environments have control flow, test frameworks, static typing, documentation, error management, and versioning. They are ideal for building contracts and enforcing them.

yoke deploys "flights" to Kubernetes (think helm charts or packages). A flight is a wasm executable that outputs the Kubernetes resources making up the package as JSON/YAML to stdout.

yoke embeds a pure-Go wasm runtime (wazero) and deploys your flight to Kubernetes. It keeps track of the different revisions for any given release and provides capabilities such as rollbacks and inspection. For ArgoCD compatibility, it can render the flight to the filesystem as raw YAML resources.

## Theme

Every Kubernetes related project needs a theme. Although K8 has historically inspired nautical themes, yoke is a slight departure from the norm as it tries to move away from the YAML centric world-view of Kubernetes. Therefore yoke has an aviation related theme. Core commands, for example, are named `takeoff`, `descent`, and `runway`. However their less whimsical aliases exist as well: `up / apply`, `down / rollback`, and `render / export`.

## Installation

From source:

```bash
go install github.com/davidmdm/yoke/cmd/yoke@latest
```

## Usage

### takeoff (deploy / up)

```bash
# deploy local yoke flight
yoke takeoff my-release ./release.wasm

# deploy local yoke flight and pass arguments to it
yoke takeoff my-release ./release.wasm -- [args...]

# deploy remote yoke flight via http
yoke takeoff my-release https://github.com/my_org/infra/releases/flight-v0.1.0.wasm -- [args...]
```

### descent (rollback / down)

TODO - under development

### runway (render / export)

TODO - under development

## Contributions

Contributions are welcome! If you encounter any issues or have suggestions, please open an issue on the yoke GitHub repository.

## License

This project is licensed under the MIT License.
