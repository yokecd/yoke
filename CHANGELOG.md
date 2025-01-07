# Changelog

> [!IMPORTANT]
> This project has not reached v1.0.0 and as such provides no backwards compatibility guarantees between versions.
> Pre v1.0.0 minor bumps will repesent breaking changes.

## (2025-01-07) atc/v0.5.0 - v0.6.0 - yokecd/v0.5.0 - yokecd-installer/v0.5.0

> [!CAUTION]
> This version contains breaking changes, and is not expected to be compatible with previous versions

- atc/testing: move setup only exit to after conversions are setup ([9c9eb1a](https://github.com/yokecd/yoke/commit/9c9eb1a02d6499e7d8430a4b50d77c123d905ec0))
- yoke: check resource ownership for releases in different namespaces ([2f0b573](https://github.com/yokecd/yoke/commit/2f0b573ea03d9aa752109e3f71b1b5f21cc0c721))
- yoke: fix atc dashboard and refactor ([c1547e7](https://github.com/yokecd/yoke/commit/c1547e7dfcdc7320a1fef419f17f3468850e69ed))
- atc: refactor mayday invocation ([4c6f626](https://github.com/yokecd/yoke/commit/4c6f626bf996dea866d35b442ac25ee2bffaa58d))
- yoke/blackbox: fix command to support releases from multiple namespaces ([6bde033](https://github.com/yokecd/yoke/commit/6bde0339561b2ed9aa51740d191cd9beb8b7b198))
- yoke: breaking change: change releases to be namespaced ([cbc42eb](https://github.com/yokecd/yoke/commit/cbc42eb5b215fca2b786b877608804b897708a31))
- yoke: breaking change: disallow multi namespace flights by default and add --multi-namespaces flag ([066c74e](https://github.com/yokecd/yoke/commit/066c74e87c491cb20c7f53744da391095e088efb))

## (2025-01-05) atc/v0.4.2 - atc-installer/v0.4.1 - v0.5.2

- atc: attempt to clear airway cache dir on recompilation ([67ff745](https://github.com/yokecd/yoke/commit/67ff745283ca1853dbfe354bc9b2451ccbfd2c29))
- atc: refactor module loading logic ([d540f74](https://github.com/yokecd/yoke/commit/d540f74b9a81cf95dc94d2a8a255d0594ea4ad50))
- atc-installer: add new options and break out logic into go-gettable package ([31cef8e](https://github.com/yokecd/yoke/commit/31cef8e7c7b2a12772836ddc3905cbe8dc029686))

## (2025-01-03) atc/v0.4.1 - atc-installer/v0.4.0 - v0.5.1 - yokecd/v0.4.1

- atc: test validation webhook for airway custom resources ([b2beb8c](https://github.com/yokecd/yoke/commit/b2beb8cbcfda9d135c32672d68116aaa929c3407))
- atc: added generic flight validation handler ([162d825](https://github.com/yokecd/yoke/commit/162d8250e956ca5c46a83c45e5a2700fb2686640))

## (2025-01-01) atc/v0.4.0 - v0.5.0 - yokecd/v0.4.0 - yokecd-installer/v0.4.0

> [!CAUTION]
> This version contains breaking changes, and is not expected to be compatible with previous versions

- yoke: takeoff with export to stdout via -out flag is hierarchical by namespace, group, version, kind, name, instead of flat ([c2bb52b](https://github.com/yokecd/yoke/commit/c2bb52bee04631a0454b60f81a59607cdb2777fb))
- yoke: breaking change: use a forward slash to separate segments in internal canonical representation of resources ([5068867](https://github.com/yokecd/yoke/commit/506886740b62ef431ed01bd160e8a43b948cfde9))
- debug: split wasi execute debugging into two separate calls for compilating and execution ([a3f98b7](https://github.com/yokecd/yoke/commit/a3f98b7cc5ed331f493593bc700d04104d1a8ed3))
- yoke: rename takeoff flag test-run to stdout ([a778860](https://github.com/yokecd/yoke/commit/a778860678a1181e54e5064e24f10dc169cfe169))

## (2024-12-31) atc/v0.3.3 - v0.4.3

- atc: on airway removal do not drop converter module as we keep CRDs around ([1bdd52f](https://github.com/yokecd/yoke/commit/1bdd52ffe23809b24ded2bd73d180fa8049b27ea))

## (2024-12-31) atc/v0.3.2 - v0.4.2 - yokecd/v0.3.2

- atc: add airway finalizer ([4eb520e](https://github.com/yokecd/yoke/commit/4eb520e402318f6a4d7ecb0f7aee1caaeb39642f))
- k9s/ctrl: simplify queue to not wait for dequeue but expose a public channel and fix watcher to exit on not found ([e9edee5](https://github.com/yokecd/yoke/commit/e9edee50576575bb76c25ab88188eb63fb30266d))
- atc: use a compiled module cache to improve performance ([ffcddae](https://github.com/yokecd/yoke/commit/ffcddaed9f913741a574613234247fe402571222))

## (2024-12-29) atc/v0.3.1 - atc-installer/v0.3.1 - v0.4.1 - yokecd/v0.3.1 - yokecd-installer/v0.3.1

- atc-installer: add a validation webhook configuration for airways and test ([b05851d](https://github.com/yokecd/yoke/commit/b05851deaa8402d05dc0e230d7088a1e334cc297))
- atc: add airway admission validation logic to check crds ([7953c38](https://github.com/yokecd/yoke/commit/7953c3884d714f3c87cb24c2806a3fe5cc1e1ffe))

## (2024-12-28) atc/v0.3.0 - atc-installer/v0.3.0 - v0.4.0 - yokecd/v0.3.0 - yokecd-installer/v0.3.0

> [!CAUTION]
> This version contains breaking changes, and is not expected to be compatible with previous versions

- atc: implement graceful shutdown for atc server ([56a7bbc](https://github.com/yokecd/yoke/commit/56a7bbced2aff4a3862a95b609bf49732a455ae0))
- atc: test conversion endpoint ([f222c3b](https://github.com/yokecd/yoke/commit/f222c3b47547ee2dce7c7d5bc36f48517c2e0a65))
- deps: update deps ([ac20ecf](https://github.com/yokecd/yoke/commit/ac20ecf83e864eebe66390bdbbc2098e7acbc164))
- atc: add conversion webhook registration when airway.spec.wasmurls.converter is present ([d5665b8](https://github.com/yokecd/yoke/commit/d5665b8b03ed22fa54b8b25e96396adb5c9032be))
- atc: add basic handler for conversions ([216a86d](https://github.com/yokecd/yoke/commit/216a86d4cfcfcc6d9eab2a8c35251f9724380279))
- atc: introduce wasmLock for locking flights and converters separately ([188bb8b](https://github.com/yokecd/yoke/commit/188bb8b0bdcb1470b9bdf7fdbe75f9664358485c))
- atc: BREAKING CHANGE: change airway.spec.wasmUrls schema and support https atc server ([22d5bcc](https://github.com/yokecd/yoke/commit/22d5bccff8805428087140ffe9621d4720e914b0))
- atc-installer: add tls certs ([6d1fdc0](https://github.com/yokecd/yoke/commit/6d1fdc04b1d873e05e8fe33d5039056b29bac0f4))

## (2024-12-19) atc/v0.2.1 - v0.3.3 - yokecd/v0.2.1

- internal/releaser: fix gh release of cli ([2ad5f27](https://github.com/yokecd/yoke/commit/2ad5f277bbc2f28c0264d6e5bcc2999671e4de04))

## (2024-12-19) atc-installer/v0.2.0 - yokecd-installer/v0.2.0

- deps: update dependencies ([99e2039](https://github.com/yokecd/yoke/commit/99e2039126b6197dc33c708a1473f4773c943ccd))
- internal/releaser: release yoke cli executables ([2e77e27](https://github.com/yokecd/yoke/commit/2e77e27f2bf136c84488e0eeaea92bbd44967216))

## (2024-12-14) v0.3.2

- yoke: fix atc back navigation from revision yaml view ([6aefe85](https://github.com/yokecd/yoke/commit/6aefe85dc85d734dcfb42f7e2f4fa9340c5c0f3a))

## (2024-12-13) v0.3.1

- yoke: fix atc back navigation in resource view ([2de8600](https://github.com/yokecd/yoke/commit/2de86002d6ac0543171952984637a787439fdaf9))

## (2024-12-13) atc/v0.2.0 - v0.3.0 - yokecd/v0.2.0

> [!CAUTION]
> This version contains breaking changes, and is not expected to be compatible with previous versions

- yoke: breaking change: change yoke internal secret naming to support atc releases that include type flight information ([654a14c](https://github.com/yokecd/yoke/commit/654a14c257226c58232e17e89d1ffa2a37f8be50))
- yoke: add initial draft of atc subcommand ([511f31b](https://github.com/yokecd/yoke/commit/511f31bb8e554c77eeb7a97699daba89db31e266))

## (2024-12-11) atc/v0.1.2

- k8s/controller: use contextual logger in event loop ([4ea004e](https://github.com/yokecd/yoke/commit/4ea004ed3e1e97448b09b94f5f32dd2d4b6d7a7a))
- atc: restart watcher on kube events being closed ([295621d](https://github.com/yokecd/yoke/commit/295621d4b72ceeb7e090371d30a31b4ae558d142))

## (2024-12-04) atc/v0.1.1 - v0.2.1 - yokecd/v0.1.1

- yoke: log to stderr after successful takeoff ([01703c6](https://github.com/yokecd/yoke/commit/01703c62dd22fd1825b7dff77f46dc084255ffeb))
- project: add code of conduct and contributing markdowns ([cc42a37](https://github.com/yokecd/yoke/commit/cc42a37732891e7000776c2f92a76f32e0705843))

## (2024-12-01) atc/v0.1.0 - atc-installer/v0.1.0 - v0.2.0 - yokecd/v0.1.0 - yokecd-installer/v0.1.0

> [!CAUTION]
> This version contains breaking changes, and is not expected to be compatible with previous versions

- internal/releaser: properly check latest tags ([7fcd88e](https://github.com/yokecd/yoke/commit/7fcd88ec39803cdeb9d9d7c13558632a2a370be3))
- yoke/testing: update tests to check labels ([390e67f](https://github.com/yokecd/yoke/commit/390e67fbebde97c0e1843f53634007fea413e689))
- internal/releaser: check for breaking changes and bump minor ([ebf677c](https://github.com/yokecd/yoke/commit/ebf677cec53b0e648380f45b31ae1dbac1872238))
- yoke: ensure flight dependecies are labeled with yoke metadata ([b9ba4b2](https://github.com/yokecd/yoke/commit/b9ba4b25bcba50d290c90f32269f0dbfdf24bd31))
- deps: update dependencies ([3baadfc](https://github.com/yokecd/yoke/commit/3baadfc203a5b673b8a750063920cd199f99cd29))
- yoke: breaking change: remove resource-mapping and use labels instead ([2941bcf](https://github.com/yokecd/yoke/commit/2941bcf583ab4957d8a7ac323d968f8352be6f17))

## (2024-11-24) atc/v0.0.2 - atc-installer/v0.0.2 - v0.1.4 - yokecd/v0.0.4 - yokecd-installer/v0.0.10

- yoke: fix segfault of wasm module close on invalid wasm inputs ([5fd75ea](https://github.com/yokecd/yoke/commit/5fd75ea23361ebadd7bd60b51290962506bf7129))

## (2024-11-24) atc/v0.0.1 - atc-installer/v0.0.1 - v0.1.3 - yokecd/v0.0.3 - yokecd-installer/v0.0.9

<details>
<summary>76 commits</summary>

- internal/releaser: tag repo after dockerbuilds ([79d1084](https://github.com/yokecd/yoke/commit/79d1084664ccfd49a4e67293eb68a2456133db08))
- internal/releaser: fix target versions for wasm and docker releases ([fc3435c](https://github.com/yokecd/yoke/commit/fc3435c608a0c6db1c2b9669f4ce8384840d30ea))
- internal/releaser: support releasing from local branch ([1596caa](https://github.com/yokecd/yoke/commit/1596caa394852fae80545b575f966c03c5be539b))
- atc/testing: make setup test compatible with nodeports for demos ([9d09c61](https://github.com/yokecd/yoke/commit/9d09c6139cbc4174d540d2f9381db29cc3aa11e8))
- workflows/pipeline: rename releaser to pipeline in workflows ([dbe8bb6](https://github.com/yokecd/yoke/commit/dbe8bb65270111ff665f8f851be502f029557ad1))
- yokecd: added yokecd suffix to yokecd Dockerfile ([08a3016](https://github.com/yokecd/yoke/commit/08a3016fafe9f1cc2aac65625a296a5dd1b94be8))
- atc: fixed mayday bug where updating status was not updating unstructuted object ([c4f1755](https://github.com/yokecd/yoke/commit/c4f1755a9a4a0e9dfc98e16c0fe120bf300b55a2))
- refactor: reorder imports ([087b4a7](https://github.com/yokecd/yoke/commit/087b4a77e9cd0d383a54874257b3cfbb3ab2f964))
- atc/testing: fix backend example selector ([019dd90](https://github.com/yokecd/yoke/commit/019dd9083ac73aa5b8849040735521477212b77d))
- internal/releaser: check against non-prefixed tag when releasing yoke cli ([8cf503e](https://github.com/yokecd/yoke/commit/8cf503ea8983bc2fe3e1620e9d6bdce1b7939267))
- internal/releaser: release yoke cli on change ([24c55f9](https://github.com/yokecd/yoke/commit/24c55f9aa727f8572d1dbe067447c8017708d3e5))
- internal/releaser: refactor ([3d22efe](https://github.com/yokecd/yoke/commit/3d22efe0ddddebac254b75d703a554fc277b2aab))
- internal/releaser: revert back to main branch instead of main commit reference ([6f18588](https://github.com/yokecd/yoke/commit/6f185884321e2ea78b17f17c5ac5909b651349f6))
- fix(git-state): bad merge/stash-apply were causing compilation errors ([903a53d](https://github.com/yokecd/yoke/commit/903a53de8e9916e5b06b86f8f53658c2926b4197))
- internal/releaser: release dockerfiles ([5b7e436](https://github.com/yokecd/yoke/commit/5b7e436a22f3068987606efe54d885b3bd850744))
- internal/releaser: compare binary data for releasing wasm ([3455567](https://github.com/yokecd/yoke/commit/3455567e89524472c1682323398f1afada6a1e5a))
- gha: install latest kind for testing ([bc7695d](https://github.com/yokecd/yoke/commit/bc7695d5adf027baf561ef5605307f9a6116e0c1))
- atc: commit wasmcache flight.wasm for compilation reasons ([65bfb92](https://github.com/yokecd/yoke/commit/65bfb92049a051b8554bc5203a26d361b644bb8d))
- atc-installer: refactor ([f2f5930](https://github.com/yokecd/yoke/commit/f2f5930c199623b62c4a239590261856656fd63e))
- internal/k8s: add airway to readiness check ([3e0d433](https://github.com/yokecd/yoke/commit/3e0d4333600da235d35aad634abbd2181ce09725))
- testing: e2e testing spawns take responsibility for spawning kind clusters ([fc8394d](https://github.com/yokecd/yoke/commit/fc8394d917b8da58e0db782f829f1eeaf4ea8fb6))
- airway: refactor ([d0a8bdd](https://github.com/yokecd/yoke/commit/d0a8bdd8d7296e7a399d18cbc2aeefac3f33a438))
- airway: support wasmUrl per served version of crd ([819bc01](https://github.com/yokecd/yoke/commit/819bc01ea7e1a0ac5c60dc438095ecfe303ba258))
- atc: set default namespace on namespaced scope flights with empty namespace field ([301bfec](https://github.com/yokecd/yoke/commit/301bfec3e97fa3d73ae20cdb5733eab207222ae8))
- atc: default to passing entire custom resource as input ([7b86581](https://github.com/yokecd/yoke/commit/7b86581713b1b5989c6624b1fc5ea5922dfac8bf))
- atc: less flaky status updates ([3074ef2](https://github.com/yokecd/yoke/commit/3074ef24797ac15847616dfe7895441f15004a28))
- pkg/openapi: support Enum and XValidations struct tags ([0417d76](https://github.com/yokecd/yoke/commit/0417d767dbdbc714d35d57ac581e258215890c3b))
- pkg/openapi: support basic validation via struct tags ([ec9c794](https://github.com/yokecd/yoke/commit/ec9c7944620de33e01196a265a5d52aaa7fe9b3f))
- atc: testing backend flight should use backendSpec ([5234d6a](https://github.com/yokecd/yoke/commit/5234d6a4793313dac7da39261cedebe0e337b480))
- k8s/ctrl: queue no longer needs concurrency hint ([119fd66](https://github.com/yokecd/yoke/commit/119fd66b81033a4e2f4e81a61fa4df23f86f2420))
- atc: first initial happy test ([ac4752d](https://github.com/yokecd/yoke/commit/ac4752ddc1cad8ac647bf231f581f19bcf1baf47))
- atc: refactor ([25820eb](https://github.com/yokecd/yoke/commit/25820ebd4de060028b57bc0a201d1c07b0b41248))
- atc: add status to flights ([2e1df1e](https://github.com/yokecd/yoke/commit/2e1df1e545f55f7d883d960265e13bc927404697))
- k8s/ctrl: unshift queue on dequeue ([4145872](https://github.com/yokecd/yoke/commit/414587245b74de69b057b033b3d8f0529bb8fc27))
- atc: do not leak any goroutines on cancelation ([113558e](https://github.com/yokecd/yoke/commit/113558e9bbfcaf1ed051e2df703ef38a58e759e5))
- atc-installer: configure labels and annotations on atc deployment ([b137cbe](https://github.com/yokecd/yoke/commit/b137cbe695e417440384b44d2c7a71d534edb897))
- k8s/ctrl: drop status events ([77ddd41](https://github.com/yokecd/yoke/commit/77ddd418519536ec564f121ca51ed52ca226a6e4))
- atc: refactor ([2465395](https://github.com/yokecd/yoke/commit/2465395edbb45ebb92c04c06aba2096146007910))
- atc: update atc readme ([30047b7](https://github.com/yokecd/yoke/commit/30047b7c72fb4e348b6bdd7448548247f202983e))
- airway: fixDriftInterfal use go time.Duration format ([9216572](https://github.com/yokecd/yoke/commit/92165725240eb34bc4a90633cae855e47ca8a6c5))
- k8s/ctrl: added jitter to exponentional backoff in reconcile loops ([0186569](https://github.com/yokecd/yoke/commit/018656913552495b030693306a9d70bcdd38a222))
- atc: rename fixDrift to fixDriftAfterSeconds ([583a288](https://github.com/yokecd/yoke/commit/583a288e239f9b7f25f97ee77168251639965006))
- atc: add fixDrift in airway configuration ([0bf391f](https://github.com/yokecd/yoke/commit/0bf391f5193ca40f2211b1f493e2bf9227ca8e00))
- atc: use createCRDs configuration ([38093d3](https://github.com/yokecd/yoke/commit/38093d3b36e3f1df8cf982d6dfc310e07d2c652b))
- atc: remove Inprogress status ([cc3073f](https://github.com/yokecd/yoke/commit/cc3073f22d946132f82ce3d6bd2f4d9ec09dd24f))
- pkg/apis/airway/v1: proper type splitting for reuse in other packages ([afe748a](https://github.com/yokecd/yoke/commit/afe748a7c7718961b1109494365953ecdbbd9cc2))
- atc-installer: make createCrds optional ([0bd56f0](https://github.com/yokecd/yoke/commit/0bd56f07d1055bae1a90cb5e2b591d7bf2e04c08))
- pkg/openapi: minimalistic kube style openapi generation ([95e6a86](https://github.com/yokecd/yoke/commit/95e6a864cf81b9bc4ca97f089589f13fc36a7cba))
- examples/kube: use k8s.io/api instead of client-go ([1ea0d43](https://github.com/yokecd/yoke/commit/1ea0d434b6714a243b3c8e2ffda6af813db4406f))
- refactor: regorganize imports accross project ([240ea82](https://github.com/yokecd/yoke/commit/240ea82b3fb8ab9ebe1e8d09947bb08cfd006a98))
- atc-installer: use apimachinery yaml package for decoding ([85ec233](https://github.com/yokecd/yoke/commit/85ec233cde56e7e05224d9b04791bf0cb642e353))
- atc: add createCrds to airway spec ([43128b7](https://github.com/yokecd/yoke/commit/43128b7735992621ed9df6de4be35cf3455cf595))
- atc: introduced typed airway ([5b5ffc2](https://github.com/yokecd/yoke/commit/5b5ffc2313f6ae7726a8ab011c46418eb7fbd106))
- atc: refactor ([f6dbdce](https://github.com/yokecd/yoke/commit/f6dbdce7dbe6ee5428d18618dc372d38f6eccc37))
- k8s/controller: don't requeue active events until reconcile is done ([0eaa7f7](https://github.com/yokecd/yoke/commit/0eaa7f7ba5bbaaa0e1c56e3f2f12214e219d7d60))
- atc: add support for airway status ([2021634](https://github.com/yokecd/yoke/commit/2021634880ff29128634d9650a3aeafdf0665d2e))
- atc: add atc teardown ([7033ef6](https://github.com/yokecd/yoke/commit/7033ef63d25cfafa2172de31d70ffdd0616b5938))
- atc: replace flight controllers on change to airway ([b822c2c](https://github.com/yokecd/yoke/commit/b822c2c12b9fce548a04de7216f8b864ec1ae759))
- atc: refactor and add loopId to reconcile loop attributes ([0c9e620](https://github.com/yokecd/yoke/commit/0c9e6200522cc224c54d19cc063d1d3cd559a519))
- k8s/controller: invalidate timers if new events are processed before a requeue is triggered ([d554dcf](https://github.com/yokecd/yoke/commit/d554dcf42b63ee6e52f56fcb82c006a3d852ea9f))
- atc: add ownerReferences to flight resources produced by atc airway underlying flight ([2ad489e](https://github.com/yokecd/yoke/commit/2ad489ecb497616640a7edb796c1696129bdec9b))
- atc: implemented cleanup finalizer for flight resources ([73e768d](https://github.com/yokecd/yoke/commit/73e768d8894242a71aaa69a71ca1ffaa2bb009c7))
- atc: use rwlock for accessing cached wasm ([27323e4](https://github.com/yokecd/yoke/commit/27323e4d65ef55eb3b68d4e5a41fe7d9c1bd81b7))
- atc: refactor logic out of main run function into atc type ([6f67787](https://github.com/yokecd/yoke/commit/6f6778759bc55132b43d6d5c8036ecde239f8279))
- atc: added inline first draft of atc controller logic ([16704dd](https://github.com/yokecd/yoke/commit/16704ddafdffe57dcfa3381024ea254a6e585832))
- atc-installer: fix airway crd scope to cluster ([4caac50](https://github.com/yokecd/yoke/commit/4caac501709dfdfb1356c4c9744b0e863ade0cb2))
- refactor: moved controller logic from cmd/atc into internals/k8s/controller ([4cd55d9](https://github.com/yokecd/yoke/commit/4cd55d9dbec02b6edce8ec469e5dff5468a9b6d3))
- atc-installer: fix airway schema ([3c2097a](https://github.com/yokecd/yoke/commit/3c2097a491419dc24e8f09382969357d995bbdec))
- atc-installer: add deployment and service account ([a962bab](https://github.com/yokecd/yoke/commit/a962bab50faf4fb8155679509d222fcf2e5d9356))
- atc-installer: add first draft of airway crd ([47d6f7e](https://github.com/yokecd/yoke/commit/47d6f7e721bd3b1b62e8972dafe9be02a6bfa9af))
- atc: fix queue to act as a proper queue ([51486c0](https://github.com/yokecd/yoke/commit/51486c063e5a6b37d9f6524629bb7a0c004c2779))
- atc: add controller with simple loop and requeue impl ([edc322f](https://github.com/yokecd/yoke/commit/edc322ff91d053bca3ee01b09f5d1ff3818122d6))
- atc: basic event queue impl ([24a9a19](https://github.com/yokecd/yoke/commit/24a9a19dba280d53ad178043b7552f89f067d344))
- atc: worker wip ([7d4fc02](https://github.com/yokecd/yoke/commit/7d4fc0293058efbc40156094d8b9187543c7a13c))
- atc: added shell for atc and atc-installer ([3beffe2](https://github.com/yokecd/yoke/commit/3beffe2557475f50a0f21af55bbea64e6bf59c76))
- cmd/atc: initial thoughts in readme ([dce5d0f](https://github.com/yokecd/yoke/commit/dce5d0f280138f41f1ba1f0587471642d167f62b))

</details>

## (2024-10-23) v0.1.2 - yokecd-installer/v0.0.8

- yokecd-installer: update installer to use argo-helm/argo-cd chart 7.6.12 ([e1085cb](https://github.com/yokecd/yoke/commit/e1085cb914413051ac204af837b7f391620c09ad))
- yokecd-installer: use IfNotPresent for image pull policy ([14e5554](https://github.com/yokecd/yoke/commit/14e555467b07d7fa7971d56ab78e7ee929fcd3e0))
- yokecd: add secret reference templating support for wasm urls ([66b6437](https://github.com/yokecd/yoke/commit/66b6437048aaefefbae4aa43e15434505f30a67d))

## (2024-10-23) yokecd-installer/v0.0.7

- ownership: update org from davidmdm to yokecd ([3a08306](https://github.com/yokecd/yoke/commit/3a08306ca923078e8cec667d99ed223957633d5b))
- yoke/takeoff: add compilation cache flag ([1c05468](https://github.com/yokecd/yoke/commit/1c054686a2fcb3e57a99c167d66d5aa7c521c724))
- deps: use wazero v1.6.0 for fast wasm compile times ([457dbe8](https://github.com/yokecd/yoke/commit/457dbe8cd08f5923c4c5baf1eaafece4f500322a))
- chore: update module version to go1.23.0 ([65dca73](https://github.com/yokecd/yoke/commit/65dca739e76dbe5a344b716e4cd370b8d01fc709))
- chore: update dependencies ([c99881a](https://github.com/yokecd/yoke/commit/c99881abf7cffd494ad1daf8711bfc3d7a12f157))

## (2024-08-22) v0.1.1

- cmd/blackbox: fix active revision when listing release revisions ([e08dd7f](https://github.com/yokecd/yoke/commit/e08dd7fb82cf2c611c20687770097c39aebda056))
- chore: update dependencies ([ec169ac](https://github.com/yokecd/yoke/commit/ec169ac9ca9aab5f142ed3396ccc6b9e29168c85))

## (2024-07-04) yokecd/v0.0.2

- yokecd: use yoke.EvalFlight instad of low-level wasi.Execute to be more compatible with pkg/Flight helpers ([87230e9](https://github.com/yokecd/yoke/commit/87230e9e720c8e386c70ea1a86782408ec46f944))
- cmd/internal/changelog: add dates to tag ([6163eae](https://github.com/yokecd/yoke/commit/6163eae045e3d5487d519414dc82b03337c5403a))
- cmd/internal/changelog: fix issue where multiple tags on same commit would only show one tag ([dae2c54](https://github.com/yokecd/yoke/commit/dae2c543adc2bad74cb8ea62bfa9a539ce2791fc))
- cmd/internal/changelog: added internal command to generate changelog for project ([c98628b](https://github.com/yokecd/yoke/commit/c98628b6443eed0029acf368e5ab12f57ad7c8ef))

## (2024-06-22) v0.1.0

> [!CAUTION]
> This version contains breaking changes, and is not expected to be compatible with previous versions

- yoke: breaking change: represent revision history as multiple secrets ([cde0d83](https://github.com/yokecd/yoke/commit/cde0d832f855f26ea51e6385677fdbd5f2d92e41))

## (2024-06-17) v0.0.11

- yoke/takeoff: switch default to true for --create-crds flag ([4ffe721](https://github.com/yokecd/yoke/commit/4ffe7218468e2b3a5897af5c2bfd42eca9439de9))
- cmd: added --poll flag to set poll interval for resource readiness during takeoff and descent ([63a6437](https://github.com/yokecd/yoke/commit/63a64376c8ac32f564144eb9ece290fa9d992e6c))

## (2024-06-16) yokecd-installer/v0.0.6

- yokecd-installer: bump argocd chart to version 7.1.3 ([ea662ae](https://github.com/yokecd/yoke/commit/ea662ae7dbd55b3ac6605bbd325578151d265588))

## (2024-06-15) v0.0.10

- deps: update project dependencies ([2785be6](https://github.com/yokecd/yoke/commit/2785be63452ff98263ebca85dd74c1bc07bdecee))

## (2024-06-15) v0.0.9 - yokecd-installer/v0.0.5

- pkg/helm: support subchart dependencies ([969e592](https://github.com/yokecd/yoke/commit/969e592ef4b8555b30c84f380b0d4a362a05620c))
- cmd/takeoff: test --wait option ([14f3c67](https://github.com/yokecd/yoke/commit/14f3c670f5508724f475e938d0db6f2d8e1fcd0d))
- pkg/yoke: add wait option to descent ([e7580be](https://github.com/yokecd/yoke/commit/e7580be0ce06d9a536c8f4686b33f3728e8688a7))
- pkg/yoke: add wait option to takeoff ([2721de8](https://github.com/yokecd/yoke/commit/2721de8060196723c78981e2896701e1671a7773))
- internal/k8s: support readiness checks for workloads resources like pods, deployments, statefulsets, replicasets, and so on ([21d2e7c](https://github.com/yokecd/yoke/commit/21d2e7c623fe752ac2e2b23c03cdb6f442857afd))
- pkg/yoke: move wasm related code into same file ([2067753](https://github.com/yokecd/yoke/commit/2067753d8e3ed607a03d54c0fc89c7ce3c1bf51e))
- yoke/debug: add debug timers to descent, mayday, and turbulence commands ([f377a27](https://github.com/yokecd/yoke/commit/f377a27580d3ead62bb19504adaba348bc11c09c))
- yoke/takeoff: wait for namespace created by -namespace to be ready ([178bf8d](https://github.com/yokecd/yoke/commit/178bf8d3e1c37d4d310c7baba0ab2a71890a8821))

## (2024-06-01) v0.0.8

- pkg/yoke: set release in env of flight; update pkg/flight accordingly ([488985e](https://github.com/yokecd/yoke/commit/488985e3aa36c7a579a5c220ddb30c17e754063d))

## (2024-06-01) v0.0.7

- cmd/yoke: add create namespace and crd logic to takeoff (#20)

* cmd/yoke: add create namespace and crd logic to takeoff
* pkg/yoke: refactor move all takeoff command logic into commander.Takeoff ([5aebdcc](https://github.com/yokecd/yoke/commit/5aebdccb99ccf63a595052b269598756c4d83faf))

## (2024-05-29) yokecd-installer/v0.0.4

- pkg/helm: do not render test files ([df2329f](https://github.com/yokecd/yoke/commit/df2329f7beb24366097c2a7547225304ebd766bf))
- yoke: use stdout to determine color defaults for takeoff and turbulence ([164c7b7](https://github.com/yokecd/yoke/commit/164c7b79e06496092fb7b0d9114ef363910d3f38))
- yoke: concurrently apply resources during takeoff ([50cad15](https://github.com/yokecd/yoke/commit/50cad159a12dee79d2461b10455bf3828151ffe4))
- yoke: rename global -verbose flag to -debug ([a9a803c](https://github.com/yokecd/yoke/commit/a9a803c4d3dbb61250ea73b852dec6aeb6d6075a))

## (2024-05-19) v0.0.6

- yoke: add takeoff diff-only tests ([824d4fb](https://github.com/yokecd/yoke/commit/824d4fb75c4c6040695a1c4a5c414ead59ffb9f7))
- refactor: stdio, consolidate use of canonical object map ([f5e2dff](https://github.com/yokecd/yoke/commit/f5e2dff4e09528d5c7f70f11b0d53ea72fecc950))
- formatting: fix import order ([124d8a6](https://github.com/yokecd/yoke/commit/124d8a67adfe4f3fdb3d4e6f5367c719c014f0f3))
- refactor: add contextual stdio for better testing ([91c8391](https://github.com/yokecd/yoke/commit/91c8391444f01216c7aadf45917d70b4148c8d70))
- yoke: update xcontext dependency ([9c6c178](https://github.com/yokecd/yoke/commit/9c6c178243cfec6dd82d716b0f173c58f6967bf9))
- yoke: use canonical object map for takeoff diffs ([7a9f0ff](https://github.com/yokecd/yoke/commit/7a9f0ffbc8d069be395cc1f88293e13796135d64))
- Added --diff-only to takeoff command (#17) ([e4c8a25](https://github.com/yokecd/yoke/commit/e4c8a258e8d99cea04c9842fae6e51e30e042307))

## (2024-05-18) v0.0.5

- yoke: drift detection ([3ab27a7](https://github.com/yokecd/yoke/commit/3ab27a7610ab869830807bbe17cd51895e8f8a6b))
- yoke: add drift detection ([3e1e2a9](https://github.com/yokecd/yoke/commit/3e1e2a98fbce95f5435e6e6f3fe1dbfd7bd87d22))
- readme: add link to official documentation ([bdf3565](https://github.com/yokecd/yoke/commit/bdf3565f8e89abb6745fe5c2a1ffa6a2d14d1217))

## (2024-05-04) yokecd-installer/v0.0.3

- yokecd-installer: make yokecd docker image version configurable ([821d6e3](https://github.com/yokecd/yoke/commit/821d6e3ee992f7ff25b75ba3ea84d55a85bae5f5))

## (2024-04-29) v0.0.4

- yoke: add namespace to debug timer ([4e8ab04](https://github.com/yokecd/yoke/commit/4e8ab04a46382649e3b52930fb0f590b2fc3a5a2))
- refactor: fix import orderings ([6d3a09f](https://github.com/yokecd/yoke/commit/6d3a09f3aed6e77fc42a9d8ff06289b91e51999a))
- yoke: ensure namespace exists before applying resources ([8cee965](https://github.com/yokecd/yoke/commit/8cee96515043712dc2caca04aea7475fa78a2506))
- yoke: fix help text mistakes ([a6657ae](https://github.com/yokecd/yoke/commit/a6657ae620d3d80c5f24c81c172e34d76b62c979))
- yokecd: remove wasm after use in build mode ([7dbd330](https://github.com/yokecd/yoke/commit/7dbd330cea6c1d3fcf51390d3c4ab257968bb520))

## (2024-04-25) v0.0.3

- yokecd: added config parsing tests ([7dc8200](https://github.com/yokecd/yoke/commit/7dc8200c13f9750cc93f093586dcec227d883a25))
- yokecd: add build mode ([8760b9f](https://github.com/yokecd/yoke/commit/8760b9f94723a161281192187bd09bc30ddfe499))

## (2024-04-21) yokecd-installer/v0.0.2

- releaser: fix patch inference ([960853a](https://github.com/yokecd/yoke/commit/960853a4ab113904077430db84081ac264685b4c))
- pkg: added flight package with convenience functions for flight execution contexts ([fd401ea](https://github.com/yokecd/yoke/commit/fd401ea19d5bb304fb4b7b45245c55e9c689c615))
- yokecd: require wasm field at config load time ([660a913](https://github.com/yokecd/yoke/commit/660a913f6ec1fae7fe216cb3a4b0c8dbb144d6a2))

## (2024-04-20) v0.0.2

- yoke: added verbose mode with debug timings for functions ([2f87cef](https://github.com/yokecd/yoke/commit/2f87cef5cf06e757f73dcc21643aa38117fe24c2))
- yoke: improve takeoff help text ([b74f17d](https://github.com/yokecd/yoke/commit/b74f17d1599a6cb7ce49b202145fed3663a5dad7))
- yoke: add wazero to version output ([af90ae6](https://github.com/yokecd/yoke/commit/af90ae6624b7687982f8f74f0ee890cc87e9ee41))

## (2024-04-20) v0.0.1 - yokecd-installer/v0.0.1

- releaser: release patch versions from now on ([eac2db4](https://github.com/yokecd/yoke/commit/eac2db4c409ab28039c4cddc86c1c4a96f380553))
- update dependencies ([44a6dd7](https://github.com/yokecd/yoke/commit/44a6dd79af61344d345804feb894a843eedb6653))
- yoke: fix force conflicts flag not propagated ([a8a086c](https://github.com/yokecd/yoke/commit/a8a086c210e04f2323b8f44289fdf138a9204186))
- yoke: interpret http path suffixes with .gz as gzipped content ([e68f8ba](https://github.com/yokecd/yoke/commit/e68f8ba32e3b96bf7dc93a342799ece3a8f8623b))

## (2024-04-16) yokecd-installer/v0.0.0-20241704012137

- yokecd: use argo-helm/argocd for installer ([a3fe4df](https://github.com/yokecd/yoke/commit/a3fe4df441404ab2a9b1225175e8ed3c2fac603c))
- yoke: use secrets instead of configmaps for storing revision state ([5e39717](https://github.com/yokecd/yoke/commit/5e397171e214463166f5facfa62050f0f60324fd))
- add tests to workflow ([01608f8](https://github.com/yokecd/yoke/commit/01608f8a5df6186fbd5522f1440b4990133db177))
- revert wazero to v1.6.0 and use compiler ([c5d48bf](https://github.com/yokecd/yoke/commit/c5d48bf0556b28df698c438747ed6d3d02a15e38))

## (2024-04-09) yokecd-installer/v0.0.0-20241004031222

<details>
<summary>11 commits</summary>

- fix compressor ([5dc59c0](https://github.com/yokecd/yoke/commit/5dc59c0721aa56bf77ceb9995dd46b5e49688446))
- fix download err ([2552acb](https://github.com/yokecd/yoke/commit/2552acbfc0a563475bc9013a8a54877535069840))
- release yokecd-installer ([ed4d68d](https://github.com/yokecd/yoke/commit/ed4d68d16f5153d2b1e399006cfd4b8faaff581e))
- release yokecd as gz ([95e83db](https://github.com/yokecd/yoke/commit/95e83db497814b20f47496394f017be5cf947ac8))
- add yokecd releaser workflow ([579ca0c](https://github.com/yokecd/yoke/commit/579ca0c0452e9346f4dfd40899dd8ca4ed727916))
- yokecd: remove leading slashes for wasm parameter ([9a69c9e](https://github.com/yokecd/yoke/commit/9a69c9e2a5a5ac99f609cc2ad0103e3ed6a51b6b))
- support one parameter wasm instead of wasmURL and wasmPath ([02245e2](https://github.com/yokecd/yoke/commit/02245e28ee7257c4765e757c5ebd9a805b41e9a2))
- yoke: add resource ownership conflict test ([8809ad7](https://github.com/yokecd/yoke/commit/8809ad730aceb2eaf8b7171bdf4f482199e85b11))
- yoke: support gzip wasms ([8d3dbb1](https://github.com/yokecd/yoke/commit/8d3dbb144650d8a68910fded2033b21b5b868302))
- yokecd: add suport for wasmPath parameter ([cfc3952](https://github.com/yokecd/yoke/commit/cfc39526323d6e295fc03ff98f299a81f90b2dba))
- test simplified yokecd application ([70c44d3](https://github.com/yokecd/yoke/commit/70c44d3bf34b8aeb005eedcbdb564cace0279492))

</details>

## (2024-03-24) v0.0.0-beta3

<details>
<summary>23 commits</summary>

- hardcode yokecd as plugin name, and simplified plugin definition ([0de06a9](https://github.com/yokecd/yoke/commit/0de06a959bde14d7432f737d60e9a77db88e79a1))
- removed yoke exec in favor of takeoff --test-run ([a88793b](https://github.com/yokecd/yoke/commit/a88793b08995540b31bb3715b08e4cb084bacbf1))
- use wazero interpreter instead of compiler ([f87011a](https://github.com/yokecd/yoke/commit/f87011a242d62d8a03658b6e55da1127cab7de70))
- fix http proto check for wasm loading ([b8cd522](https://github.com/yokecd/yoke/commit/b8cd522c7f82bf69a2feb3838d47d86611d14358))
- added yoke exec for debugging wasm ([e77f607](https://github.com/yokecd/yoke/commit/e77f6078b503753996b2ea6dc21cdad6ca210dd1))
- revert wazero to v1.6.0 ([5402e44](https://github.com/yokecd/yoke/commit/5402e44fc4911acf191c25b885f6f90aae643ec9))
- updates deps ([e69bbfd](https://github.com/yokecd/yoke/commit/e69bbfdea0c0fd4a6f779cfed4b0d035bf9d0295))
- make flight marshalling less verbose by omitting app source ([2eaa0db](https://github.com/yokecd/yoke/commit/2eaa0db79666308c2a7b18487dda5dd25936c65d))
- remove website ([407617c](https://github.com/yokecd/yoke/commit/407617c7a816bd6714cfa8ea469ddc22a3ff08d4))
- update debug logs ([da35b1e](https://github.com/yokecd/yoke/commit/da35b1ea32eb2ccf7c97b742003b29d56f15a338))
- fix sync policy support ([0c99311](https://github.com/yokecd/yoke/commit/0c993113f8563e0360079f1ded923fec05c5dca7))
- add basic syncPolicy support ([9361903](https://github.com/yokecd/yoke/commit/9361903b8cdbff65d6e37e681ab6cde5d1f4a210))
- add plugin parameter ([602a6e7](https://github.com/yokecd/yoke/commit/602a6e79eaf4e2abec50a21b4b442d812246ec78))
- fixed flight rendering logic in yokecd ([12351c1](https://github.com/yokecd/yoke/commit/12351c190aae3030dc7f7b5bab7954be86b7e1a4))
- make flight spec embed application spec ([fd744d5](https://github.com/yokecd/yoke/commit/fd744d52dd3bd7c57b46ffdb1511d49e3986030c))
- yokecd in progress ([541cdac](https://github.com/yokecd/yoke/commit/541cdac548f54caf65faaa0ebcb15dfdf812bc51))
- add more debug info to yokecd ([34e269a](https://github.com/yokecd/yoke/commit/34e269ae906afe24d96132f2524c801ff09c80f5))
- add yokecd example flight with patched argocd-repo-server ([d24024b](https://github.com/yokecd/yoke/commit/d24024b123b2aa60cc21c0e2a32718c32572ae03))
- fix go.sum ([1327974](https://github.com/yokecd/yoke/commit/13279741f03bceccc38e0481dbcede48bb497abd))
- basic code for yokecd ([396ccfc](https://github.com/yokecd/yoke/commit/396ccfc4d2a4cb6002e8e273f583674af18f38f7))
- add version to helm2go ([08bfcac](https://github.com/yokecd/yoke/commit/08bfcaca2c3a61f4733cc89aa7b303840b5970d8))
- update helm2go to default to map[string]any if cannot generate values type ([48b3a22](https://github.com/yokecd/yoke/commit/48b3a22d16b9f4bbd788865b077fecc695488756))
- add force-conflicts flag for takeoff ([c058acb](https://github.com/yokecd/yoke/commit/c058acb49961f7a67822a4e928cd069d237aa776))

</details>

## (2024-03-15) v0.0.0-beta2

<details>
<summary>14 commits</summary>

- use server-side apply patch ([68f1d97](https://github.com/yokecd/yoke/commit/68f1d9716d0f29788aef1a831a9d958a94bcc98d))
- use official argocd install yaml for argo example ([bb46eb3](https://github.com/yokecd/yoke/commit/bb46eb3d25ef9fe2492884a311ce83fbb595c35c))
- try create before apply ([172fc7f](https://github.com/yokecd/yoke/commit/172fc7f758e58afeacf4138d9fba247551d85149))
- support non-namespaced resources ([abcd57a](https://github.com/yokecd/yoke/commit/abcd57a4feab395111d54e11ced9d9d36acc3dd7))
- add skipDryRun and namespace flags to takeoff ([86d2081](https://github.com/yokecd/yoke/commit/86d2081017d3645a73afa93885458dddd24e5a74))
- minor refactor of k8s client ([1c5f65a](https://github.com/yokecd/yoke/commit/1c5f65a2c9a126209cfc9a9a96644748e5f2477e))
- udpated helm2go output and added mongodb example ([7227a24](https://github.com/yokecd/yoke/commit/7227a24cae80035f9928004f84f68c4dc41f0771))
- add schema bool flag to helm2go ([cd9b074](https://github.com/yokecd/yoke/commit/cd9b074220060e1819432a9d29bdccaba0f6a927))
- helm2go: infer from values ([a00cb9c](https://github.com/yokecd/yoke/commit/a00cb9cc68f9582e61b67e7686e5e77556667e65))
- redis example uses generated flight from helm2go ([ce6a82e](https://github.com/yokecd/yoke/commit/ce6a82e6e6eb9dd9fbf9088a24dffc7263976552))
- helm2go generates flight package instead of type file ([b123bb1](https://github.com/yokecd/yoke/commit/b123bb1331905f61a554ac622629b84664784265))
- refactored helm2go ([5326c47](https://github.com/yokecd/yoke/commit/5326c47323a8057d0a3aaf5b623cb986b0ea95b7))
- generated pg values.go using helm2go ([0c360bb](https://github.com/yokecd/yoke/commit/0c360bb9cd7c1665b5150ecc7a4746f6029b9544))
- renamed platters to flights and added helm2go script ([9da0265](https://github.com/yokecd/yoke/commit/9da02655f5e3985e18c3343ff64e7b589bd83735))

</details>

## (2024-02-29) v0.0.0-beta1

<details>
<summary>21 commits</summary>

- starting website ([dd8c995](https://github.com/yokecd/yoke/commit/dd8c99584130ab84915a6bf5cc7e5c36af8de2a1))
- added apply dry run failure test ([10c65b7](https://github.com/yokecd/yoke/commit/10c65b7b86d5f7976e4a6ff3e47ec262c8d50748))
- remove .DS_Store ([f52068a](https://github.com/yokecd/yoke/commit/f52068a3ca6bf6fcfefb8631284f86718fb994c3))
- fix go.sum ([ae126b4](https://github.com/yokecd/yoke/commit/ae126b4f6d917ecc8d962966831d98a859230c76))
- refactored tests to not use TestMain ([7a5213f](https://github.com/yokecd/yoke/commit/7a5213f6be100fc50c65e5efd9f6a2f658c62a39))
- formatting ([607b346](https://github.com/yokecd/yoke/commit/607b3462b5523fc18205cabadf1c0de40c043229))
- remove .DS_Store ([1400e5b](https://github.com/yokecd/yoke/commit/1400e5b0f419cf6e1a670d6b5a0362b884261ada))
- the great renaming to yoke ([578ac2c](https://github.com/yokecd/yoke/commit/578ac2cef7070fc234ab83058b23eab72248ef5a))
- ported descent and mayday to sdk ([a44cf26](https://github.com/yokecd/yoke/commit/a44cf26e154ab52b85ad19cba6e057fa3547859c))
- started sdk restructuring ([6a58b9b](https://github.com/yokecd/yoke/commit/6a58b9bc83baa99734915b30cc04e2f932f2566c))
- add export to stdout ([44b071f](https://github.com/yokecd/yoke/commit/44b071f4bd25c489fa900f8bbda166039ea0ae2f))
- rename k8 to k8s ([3223e8b](https://github.com/yokecd/yoke/commit/3223e8b3f81f43e22a161efa978c754fc9c04ed4))
- refactor kube example ([a5f85c4](https://github.com/yokecd/yoke/commit/a5f85c4dbd6aa9d8d4d7ec00de759e7ffb474a4e))
- refactored example platters around ([30067fc](https://github.com/yokecd/yoke/commit/30067fcf220410e5e1ea808908d4f143dc32b93c))
- wrote first test ([7f4e9a9](https://github.com/yokecd/yoke/commit/7f4e9a9150f8173a34a3b498475155f3a389addf))
- add blackbox --mapping flag ([8506be6](https://github.com/yokecd/yoke/commit/8506be61b987dd101265647ebbae98680ace479f))
- use all prefix for embedding private templates in helm expanded example ([f1850bf](https://github.com/yokecd/yoke/commit/f1850bfd99bdde4a74cb788c913290586396f4ad))
- load helm chart from embed.FS work in progress ([0ce494e](https://github.com/yokecd/yoke/commit/0ce494e2e736958823ddca201263c968ede65b58))
- added load chart from FS: wip ([70b3cee](https://github.com/yokecd/yoke/commit/70b3cee1b6576df1ee5d14160b8e7046f6991621))
- update helmchart example to make it configurable ([c5133ef](https://github.com/yokecd/yoke/commit/c5133ef509b318a3fd47eaafc8426b0e7ce0d844))
- update halloumi metadata ([11c9b2e](https://github.com/yokecd/yoke/commit/11c9b2e654f2eb486af4e5fcf61d191ad5937771))

</details>

## (2024-02-25) v0.0.0-alpha17

- updated helm api ([964d147](https://github.com/yokecd/yoke/commit/964d147b1171920142533a87ce3868a23e2dccd1))
- initial support for helm chart compatibility ([d3c926e](https://github.com/yokecd/yoke/commit/d3c926e94022635bab35f32d91108df675e1d7e5))

## (2024-02-24) v0.0.0-alpha16

- update verison command to show k8 client-go version as well ([831fdd7](https://github.com/yokecd/yoke/commit/831fdd7d5573cb1bda1b7c4f28d500d1403bec79))
- change diff indentation ([f3173be](https://github.com/yokecd/yoke/commit/f3173be28d44754138e929b83245d0b103538970))

## (2024-02-24) v0.0.0-alpha15

- print diff between revisions ([706e050](https://github.com/yokecd/yoke/commit/706e0501a4b9789fae2801249ef7da9fe0cb3187))
- refactored revision source ([6aa96a5](https://github.com/yokecd/yoke/commit/6aa96a5c38a54559ee93a7990f53b68bf0a0ccfa))
- added shas to revisions ([ce5a7da](https://github.com/yokecd/yoke/commit/ce5a7da3531078c527eedcd5e09522f5a800e1b3))
- refactor blackbox ([fc4ad5a](https://github.com/yokecd/yoke/commit/fc4ad5a722f885309e9054b37a3ecaf5c1d66cbf))
- update blackbox output ([505e281](https://github.com/yokecd/yoke/commit/505e281d35810e3b80966d06c948dd3e210626bf))

## (2024-02-24) v0.0.0-alpha14

- added mayday command ([d982624](https://github.com/yokecd/yoke/commit/d982624ea20bb8dfd6b7702a13e96717797e507e))
- remove unnecessary newline from error ([2702985](https://github.com/yokecd/yoke/commit/27029855fe5e16f1067c89c29fff4727881953d3))

## (2024-02-23) v0.0.0-alpha13

- finish first pass at blackbox command ([558273b](https://github.com/yokecd/yoke/commit/558273b069e75547c672ba19e881cd25b7b16c6d))
- update deps and formatting ([05b5096](https://github.com/yokecd/yoke/commit/05b5096d29158e5ba26e701da4222360e978ceec))
- blackbox under construction ([91d3fa7](https://github.com/yokecd/yoke/commit/91d3fa78c37795b21e3b0b626a1ea0c5393ea647))
- removed resource utility package in favor of applyconfigurations ([2de98b0](https://github.com/yokecd/yoke/commit/2de98b0ed2679917f0f7cec389df3596538caaff))

## (2024-02-23) v0.0.0-alpha12

- create an ownership check ([8c2d7f9](https://github.com/yokecd/yoke/commit/8c2d7f9f4993e0e1c3140a991f8206ce9adca570))
- added blackbox shell ([3c34f8c](https://github.com/yokecd/yoke/commit/3c34f8c6c44a8ba6acd480fbfe39640ed46fec45))

## (2024-02-21) v0.0.0-alpha11

- first working pass of descent command ([91cc860](https://github.com/yokecd/yoke/commit/91cc86088b8f82c3939089398731fb4480284581))
- first pass at descent command ([c71368b](https://github.com/yokecd/yoke/commit/c71368bbdc8e953674e7f5f8533ea639754e8424))
- modified configmap structure ([cb0691f](https://github.com/yokecd/yoke/commit/cb0691fc4ae2363f0dea13c0595f806c5f38286e))
- dynamic platter example ([043358a](https://github.com/yokecd/yoke/commit/043358a269737ad7dfd53e302b7c2c4dd92d705f))

## (2024-02-19) v0.0.0-alpha10

- updated canonical name to include api version and changed deploy to apply ([78980fb](https://github.com/yokecd/yoke/commit/78980fb1b7f434d32417bf0ac33c9316faf4dcc4))
- adding to resource utility package ([4bf13be](https://github.com/yokecd/yoke/commit/4bf13be489cdef9861ac2205002084e8aeeb1d55))

## (2024-02-18) v0.0.0-alpha9

- do not apply identical revisisions but do a noop ([750de31](https://github.com/yokecd/yoke/commit/750de31c9907f162a574bb5bf08b803a4da2e3a6))
- added beginning of a basic utility package for resource definitions ([cd43c11](https://github.com/yokecd/yoke/commit/cd43c1173f568dbabfe10015c79cf70f73e5dc82))

## (2024-02-18) v0.0.0-alpha8

- allow wasm executable to receive stdin as input ([d7d9922](https://github.com/yokecd/yoke/commit/d7d992296e40451d9ea596976cbd27be007301dc))

## (2024-02-18) v0.0.0-alpha7

- add outdir option to takeoff instead of render or runway command ([c860e31](https://github.com/yokecd/yoke/commit/c860e315edbe951695d05ac238a1f6ffa5f860f8))

## (2024-02-18) v0.0.0-alpha6

- support yaml encodings of platters ([8f4cde7](https://github.com/yokecd/yoke/commit/8f4cde72f2cd506ebdf9e18ba09e8c49b041c86d))

## (2024-02-17) v0.0.0-alpha5

- add single or multi resource platter support and stdin source support ([20fc25f](https://github.com/yokecd/yoke/commit/20fc25fcb849627707e63edf0c6b8fc0213e75bb))
- fix newline after root help text if no command provided ([2d4e04f](https://github.com/yokecd/yoke/commit/2d4e04fdd0dd23998896de8899cd3f43d4c16654))

## (2024-02-17) v0.0.0-alpha4

- small refactoring ([acc3351](https://github.com/yokecd/yoke/commit/acc335177e86337209fc2a2746398df8d03871be))
- add dry run before applying resources ([3cb6d54](https://github.com/yokecd/yoke/commit/3cb6d54736c4d80c3331913bc9c95b50e2dea8aa))
- add halloumi logo to readme ([f98fb12](https://github.com/yokecd/yoke/commit/f98fb129e683b8b1fbf7ae72ce4c00a65ee69b5b))
- update readme ([087d62c](https://github.com/yokecd/yoke/commit/087d62c6e6adb6ecae29d3c26f24bebdb3079332))

## (2024-02-17) v0.0.0-alpha3

- added readme, license, and more aliases ([a8e6152](https://github.com/yokecd/yoke/commit/a8e615276d55e64debe3a73048c8ad12974f37d6))

## (2024-02-17) v0.0.0-alpha2

- go directive 1.22 ([b965e58](https://github.com/yokecd/yoke/commit/b965e584bae7252d4c87f14a5ad87a0df7642c27))

## (2024-02-17) v0.0.0-alpha1

<details>
<summary>22 commits</summary>

- added root command help text ([26c24a3](https://github.com/yokecd/yoke/commit/26c24a36f09a724187c6a2fcfef913f67aefaee4))
- takeoff help text ([11a8602](https://github.com/yokecd/yoke/commit/11a86027ec3cee286869a67e9ddd870c8caac352))
- formmatting ([94f9345](https://github.com/yokecd/yoke/commit/94f9345f4a30ff9808239dcc3dd39cc359046df2))
- remove wasibuild go utility and replace with task file ([61ca598](https://github.com/yokecd/yoke/commit/61ca59846263e3aa670d6d7f9ce569b586ea4593))
- refactored into subcommands ([2db7d5d](https://github.com/yokecd/yoke/commit/2db7d5dc2a234b94812f67508d3c10a473f5ea7b))
- remove orphans ([7efdb5d](https://github.com/yokecd/yoke/commit/7efdb5dfb05c4d2bad5e87cbd0f2c952af7e8f33))
- save revisions as unstructured resources ([51bab9b](https://github.com/yokecd/yoke/commit/51bab9b8b1d4d75d4f1ba755f45ccb4cc419e8c5))
- add halloumi-release-label ([51d98f7](https://github.com/yokecd/yoke/commit/51d98f78744c659862a920e068e08e3b4f2a7c80))
- add revisions ([55bf01e](https://github.com/yokecd/yoke/commit/55bf01ed5db36c2ed0020790d6c0be6988d1ec54))
- update deps ([2a6072b](https://github.com/yokecd/yoke/commit/2a6072b420cc4de1a5e676a9337feffb49ac405a))
- namespace support ([d4aee29](https://github.com/yokecd/yoke/commit/d4aee296cd6a9016596effd6bd0d9403bb7152ad))
- basic annotations ([405dd75](https://github.com/yokecd/yoke/commit/405dd75d224b4cd0d96802020d5274b0820e60bb))
- k8 successful basic apply ([33515ae](https://github.com/yokecd/yoke/commit/33515aeafa46ff2c5bfb272456203123cb607d36))
- k8: wip ([e846862](https://github.com/yokecd/yoke/commit/e8468620e6da606f347b8f4814bb2b6cc7ab1190))
- refactor ([5a59247](https://github.com/yokecd/yoke/commit/5a59247f3787bb7fb7e55d9a1c110e9735482c14))
- allow haloumi packages to be invoked with flags ([a368fce](https://github.com/yokecd/yoke/commit/a368fcecbe6c4898d2c403c08de347974ba3f006))
- add .gitignore ([bee5929](https://github.com/yokecd/yoke/commit/bee5929fd62250548b231fe805180152fe0a8368))
- refactor ([379d1fb](https://github.com/yokecd/yoke/commit/379d1fbbf2e4f365ba667820c649526e16ced209))
- small utility for building wasi ([8bc22d5](https://github.com/yokecd/yoke/commit/8bc22d586bfd14229f68fb334401b2108ef8884a))
- first haloumi binary working ([08bfa45](https://github.com/yokecd/yoke/commit/08bfa45dca5f21d1d8875962f1531838337e96da))
- starting haloumi ([47b28fc](https://github.com/yokecd/yoke/commit/47b28fcfc3766575eab80a1c9e640bc33d5ffa28))
- initial wazero runtime ([ac081a8](https://github.com/yokecd/yoke/commit/ac081a89136e9e57abb27ac3797fc72095db6af9))

</details>

