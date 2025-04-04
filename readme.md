# yoke - Infrastructure-as-Code (IaC) Package Deployer for Kubernetes

## Overview

yoke is a Helm-inspired infrastructure-as-code (IaC) package deployer.

The philosophy behind yoke is that Kubernetes packages should be described via code. Programming environments have control flow, test frameworks, static typing, documentation, error management, and versioning. They are ideal for building contracts and enforcing them.

yoke deploys "flights" to Kubernetes (think helm charts or packages). A flight is a wasm executable that outputs the Kubernetes resources making up the package as JSON/YAML to stdout.

yoke embeds a pure-Go wasm runtime (wazero) and deploys your flight to Kubernetes. It keeps track of the different revisions for any given release and provides capabilities such as rollbacks and inspection.

## Theme

Every Kubernetes related project needs a theme. Although K8 has historically inspired nautical themes, yoke is a slight departure from the norm as it tries to move away from the YAML centric world-view of Kubernetes. Therefore yoke has an aviation related theme. Core commands, for example, are named `takeoff`, `descent`, `blackbox` and `mayday`. However their less whimsical aliases exist as well: `up / apply`, and `down / rollback`.

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
