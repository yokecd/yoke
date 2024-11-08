# Air-Traffic-Controller (ATC)

The air traffic controller (referred to as `atc` henceforth), is designed to be a server-side component for defining Airways which create an assoication between flights and CRDs.
This allows users to implement the behavior of CRs without having to implement a controller.

## Why not use the yoke CLI directly?

The yoke CLI is a fun piece of software, and is extrememly useful for kicking off server-side systems such as argocd or another server-side component such as the atc. Client-side package managers have fallen from popularity as kubernetes has matured. This is partially due to the rise in popularity of the pull vs push model of gitops, but also and perhaps even more importantly, the increasing popularity of CRDs and controllers. This makes sense as controllers and CRDs are meant to be the contract between kubernetes operators and users. Moreover these approaches make the state of our clusters more explicit and semantically rich.

## What does this imply for yoke?

Many applications are appropriately described as a collection of resources, and need little to no custom logic beyond have all resources deployed together. Therefore it is still useful to define CRDs that represent collections of resources.

## Why the ATC?

Client-Side Package Managers are not native to kubernetes. When we install a helm chart, or takeoff a yoke flight, the CLI deploys resources to our cluster and does some book-keeping but these resources are essentially free-form deployed into the cluster.

This is one reason among other things that ArgoCD is popular. It allows us to encapsulate a Chart or Flight inside of a generic container resource. The downside is that we are not taking advantage of the CRDs and creating Native Kubernetes APIs for our applications. We are creating generic Application resources and dumping freeform helm values and configuration that is not validated by the Kubernetes API Server.

With the ATC we can define our own CRD APIs via Airways and implement them as a code package (Flight) without having to implement an entire controller. 

## How does it work?

### Airway
An airway is a meta-level flight, and its goal is to allow users to define their own CRDs that the atc will then listen for, and associate it to a package. This will allow users to expose their flights as native kubernetes APIs.

