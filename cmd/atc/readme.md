# Air-Traffic-Controller (ATC)

The air traffic controller (referred to as `atc` henceforth), is designed to be a server-side component for managing yoke flights.

## Why not use the yoke CLI directly?

The yoke cli is a fun piece of software, and is extrememly useful for kicking off server-side systems such as argocd or another server-side component such as the atc. Client-side package managers have fallen from popularity as kubernetes has matured. This is partially due to the rise in popularity of the pull vs push model of gitops, but also and perhaps even more importantly, the increasing popularity of CRDs and controllers. This makes sense as controllers and CRDs are meant to be the contract between kubernetes operators and users. Moreover these approaches make the state of our clusters more explicit and semantically rich.

## What does this imply for yoke?

Many applications are appropriately described as a collection of resources, and need little to no custom logic beyond have all resources deployed together. Therefore it is still useful to define CRDs that represent collections of resources.

## CRDs the ATC aims to manage?


### Flight
The first CRD that yoke wants to create is without surprise the Flight. A flight should be in essense very similar to an ArgoCD Application, but represents a wasm based package. A flight should point to the wasm asset either as a url or a location in a git repository. Additionally it may have first class support for building Go wasm programs such as YokeCD does.

### Airway
An airway is a meta-level flight, and its goal is to allow users to define their own CRDs that the atc will then listen for, and associate it to a package. This will allow users to expose their flights as native kubernetes APIs.

(The mechanic for linking a flight package to a CRD is still in up for debate.)
