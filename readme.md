# yoke - Infrastructure-as-Code (IaC) Package Deployer for Kubernetes

## Overview

Yoke is a Helm-inspired infrastructure-as-code (IaC) package deployer designed to provide a more powerful, safe, and programmatic way to define and deploy packages. While Helm relies heavily on static YAML templates, Yoke takes IaC to the next level by allowing you to leverage general-purpose programming languages for defining packages, making it safer and more powerful than its predecessors.

Kubernetes packages should be described via code. Programming environments have control flow, test frameworks, static typing, documentation, error management, and versioning. They are ideal for building contracts and enforcing them.

Yoke deploys "flights" to Kubernetes (think helm charts or packages). A flight is a wasm executable that outputs the Kubernetes resources making up the package (as JSON or YAML) to stdout.

Yoke embeds a pure-Go wasm runtime (wazero) and deploys your flight to Kubernetes. It tracks revisions for each release and supports capabilities like rollbacks and inspection.

## Theme

Yoke has an aviation related theme. Core commands, for example, are named `takeoff`, `descent`, `blackbox` and `mayday`. However their less whimsical aliases exist as well: `up / apply`, and `down / rollback`.

## Installation

From source:

```bash
go install github.com/yokecd/yoke/cmd/yoke@latest
```

With Homebrew:

```bash
brew install yoke
```

## Documentation

Official documentation can be found [here](https://yokecd.github.io/docs)

## Community

Join yoke's [Discord Server](https://discord.com/invite/tHCRKg6s7Z) to discuss the yoke project, socialize, or share memes!

## Versioning

This project is still pre version 1.0.0

The project uses semantic versioning but due to is pre 1.0.0 state, breaking changes are represented as minor bumps and all other changes patches until the release of yoke v1.0.0

## Contributions

Contributions are welcome! If you encounter any issues or have suggestions, please open an issue on the yoke GitHub repository.

## License

This project is licensed under the MIT License.
