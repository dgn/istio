# Istio Proxy Conformance Suite (PCS)

Black-box tests that run one set of implementation-neutral features against every proxy Istio
supports in a role, to produce definitive data on what each implementation supports. Design:
`architecture/tests/proxy-conformance.md`.

Currently covered:

- **ingress gateway** (north-south): Envoy (`istio` GatewayClass) and agentgateway
  (`istio-agentgateway`).
- **egress gateway**: Envoy, reached through Istio API config (`Gateway` selecting the gateway's
  labels). Each run deploys its own gateway (`DeployEgressGateway`: a Deployment injected with Istio's
  gateway template, like `istio-egressgateway`), so the installation needs none. The egress feature
  backlog is `architecture/tests/egress-use-cases.md`.
- **sidecar**: Envoy, client and server sidecar-injected.
- **node**: ztunnel, the ambient L4 proxy.
- **waypoint**: Envoy and agentgateway waypoints, through a waypoint attached to the destination
  service (ztunnel is present, as in production).

Each role is its own test (`Test<Role>Conformance`) and each feature declares the roles it applies
to. There is no combinatorial "ambient vs sidecar" axis: ambient is node + waypoint, and both are
covered by their own roles. Mode parity and mixed-mode interop are separate, smaller suites, still
to do — see `architecture/tests/proxy-conformance.md` §3.9.

## How it works

The suite is a pipeline of four stages. Only writing tests needs judgment (a person, or an LLM with
review); everything around it is computed and checked on every PR:

| Stage | What | Where | How |
|---|---|---|---|
| **Sources** | Where behavior is defined: istio/api protos, annotation and label registries, istiod feature flags, the Gateway API extension registry, and reviewed interactions | `coverage/surface.go` | deterministic, except `interactions.yaml` (reviewed by hand) |
| **Surface** | One entry per field, enum value, annotation, label, flag, extension or interaction, with its `source` | `coverage/data/surface.yaml` (generated) | deterministic |
| **Coverage** | Each surface entry's status (`covered`, `exempt`, `todo`), scope, covering features and gaps | `coverage/data/coverage.yaml` (generated) | deterministic |
| **Tests** | Features: black-box behaviors, each listing the roles it applies to and the surface entries it `Covers` | `features_<area>_test.go`, `coverage/data/catalog.yaml` (generated) | person or LLM; `pcs worklist`/`scaffold` prepare the work |
| **Report** | Each feature run against every proxy of a role, classified, and compared with the proxy's declared support | `pcs-report-<role>.yaml`, `declared/<role>/<impl>.yaml`, `pcs report` | deterministic |

The current numbers (surface size by source and status, features by role, per-role gaps, ratchets) are
in the generated `coverage/data/summary.yaml`; this README and the design doc do not repeat them.

## Scope

Only **Istio-specific** behavior is tested here:

- Istio APIs on gateways: AuthorizationPolicy, RequestAuthentication, PeerAuthentication,
  DestinationRule, ServiceEntry, Telemetry, Istio Gateway/VirtualService, ProxyConfig `gatewayTopology`.
- Istio extensions of Gateway API: annotations, listener `tls.options`, `Hostname` backendRefs,
  parametersRef ConfigMap overlays, manual deployment, the deployment controller.
- Istio data-plane defaults: XFF/XFCC handling, request ids, retries, timeouts, metrics, access logs
  (`coverage/data/defaults/defaults-inventory.yaml` maps each default istiod configures to a feature).

Plain Gateway API semantics are **not** re-tested: `TestGatewayAPIConformance` runs the upstream
Gateway API conformance suite against every proxy with *all* gateway features enabled, so a failing
upstream test means "not supported by that proxy".

Envoy-only extension APIs (EnvoyFilter, TrafficExtension) appear as `impl-specific.*` features: they
are expected to fail on other proxies and are not conformance requirements. WasmPlugin is not
Envoy-specific (its API has no Envoy-isms) and is regular surface.

## Running

Requires a cluster. Proxy images must include `nft`/`iptables-nft` if the host kernel lacks legacy
iptables (use upstream images: leave `HUB`/`TAG` unset).

```sh
# one role, all proxies
go test -tags=integ -run TestIngressConformance ./tests/conformance/proxy/
go test -tags=integ -run TestEgressConformance ./tests/conformance/proxy/
go test -tags=integ -run TestSidecarConformance ./tests/conformance/proxy/
go test -tags=integ -run TestNodeConformance ./tests/conformance/proxy/
go test -tags=integ -run TestWaypointConformance ./tests/conformance/proxy/

# one proxy, one area
go test -tags=integ -run 'TestIngressConformance/agentgateway/security' ./tests/conformance/proxy/ \
  -istio.test.pcs.proxies=agentgateway

# upstream Gateway API conformance, per proxy (slow)
go test -tags=integ -run TestGatewayAPIConformance ./tests/conformance/proxy/
```

The node and waypoint suites (and any ambient path) need an ambient installation; they skip when
the suite runs against a cluster without ztunnel.

To rerun only the features a branch changed (features, their helpers, or the outcome taxonomy):

```sh
make pcs.changed                       # prints the -run expression per role
make pcs.changed PCS_ARGS="-base HEAD~3"
```

Subtests are named `<proxy>/<feature-id>`, with the runs used for classification below them
(`run`, and on failure `rerun` and `control`).

### Outcomes, declarations and reports

Each result is classified as `supported`, `partial`, `rejected`, `fail-closed`, `fail` or `ignored`
(or `flaky`/`indeterminate`, which are test problems), and compared with the proxy's declared support
in `declared/<role>/<impl>.yaml`:

- observed matches the declaration: **pass** (supported) or **skip** (known gap);
- worse than declared: **fail** (regression);
- better than declared: **fail** until the manifest is updated;
- flaky, indeterminate, or a security feature silently ignored without a declared `ignored`:
  **fail** regardless;
- `unknown`: skipped and reported.

So the suite is green for an implementation that is exactly as conformant as it declares. Flags:

```sh
-istio.test.pcs.update-declared   # rewrite declared/<role>/<impl>.yaml from this run; review the diff
                                  # (against the reference, also records each feature's applied kinds)
-istio.test.pcs.classify=false    # no rerun/control on failure (faster, failures classified as fail/rejected)
-istio.test.pcs.report-dir=DIR    # where pcs-report-<role>.yaml goes (default $ARTIFACTS)
-istio.test.pcs.max-parallel=N    # at most N features at once per proxy (small clusters: pods per node, key quotas)
```

The report (`pcs-report-<role>.yaml`) lists per implementation a per-profile summary and per feature
the outcome, reason, failed assertion (`failedAt`, file:line), what the proxy answered there
(`failedWith`, e.g. `codes=404`), cases, declaration and decision. A failing feature is `ignored` only
when its negative control (the same feature with its config removed) fails at the same assertion
with the same answer: a proxy that answers 503 where the control got 404 did something with the
config. A run that passes but never set a key it lists in `Covers` (in the Istio config it applied)
fails: coverage needs evidence. `TestGatewayAPIConformance` writes the upstream suite's report per
proxy to `gateway-api-report-<proxy>.yaml` next to it.

`make pcs.report PCS_ARGS="-role ingress -reports DIR"` joins the surface with the reports (or, without
`-reports`, the declarations): one row per surface entry with the features covering it and each
proxy's worst result. See `architecture/tests/proxy-conformance.md` §3.6–3.7.

## Writing a feature

Start from the backlog: `make pcs.worklist PCS_ARGS="-source proto -prefix AuthorizationPolicy"` lists
`todo` surface entries (with `-role ingress`: the role's gaps, i.e. entries in its scope that no
feature of the role covers) with their documentation (proto comments, flag, annotation and
interaction descriptions), and `-o yaml` gives the same as input for an LLM.
`make pcs.scaffold PCS_ARGS="-role ingress AuthorizationPolicy.rules.to.operation.notPorts"` prints a
`Feature` skeleton with the ID, `Covers` and the keys' documentation, ending in `t.Fatal("TODO: implement")`.

Features live in `features_<area>_test.go`, by the first segment of their ID (`security.*` in
`features_security_test.go`, `backend.*` in `features_backends_test.go` or
`features_destinationrule_test.go`); `TestFeatureFiles` enforces the mapping.

Write the behavior once, against a `Target` (`target_test.go`), and list every role where it means
the same thing:

```go
Feature{
    ID:          "security.authz.deny.path",
    Description: "AuthorizationPolicy DENY on paths rejects matching requests with 403.",
    Roles:       l7Roles, // ingress, sidecar, waypoint
    RequiresL7:  true,
    Covers:      []string{"AuthorizationPolicy.action=DENY", "AuthorizationPolicy.rules.to.operation.paths"},
    Run: func(t framework.TestContext, s *Scenario) {
        tg := s.Target(t, TargetOptions{}) // routes .TargetHost to .DestV1 and checks it works
        s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - to: [{operation: {paths: ["/deny*"]}}]
`))
        tg.Expect(t, Request{Path: "/deny/x"}, check.Status(403))
        tg.Expect(t, Request{Path: "/allow"}, check.OK())
    },
}
```

`s.Target` creates what the role needs (a dedicated or shared gateway on ingress; the feature's own
backend on sidecar, waypoint and node) and sets template variables, so config is role-neutral:

| | ingress | sidecar / node | waypoint |
|---|---|---|---|
| traffic | client → gateway | meshed client → backend Service (intercepted) | ambient client → ztunnel → waypoint → backend |
| `.TargetHost` (requests, routing) | `vs.<Host>` or `<Host>` | backend Service | backend Service |
| `tg.VirtualService(http)` | bound to an Istio Gateway selecting the gateway pods | `gateways: [mesh]` | `gateways: [mesh]` |
| `tg.HTTPRoute(rules)` | parentRef the Gateway | parentRef the Service (GAMMA) | parentRef the Service |
| `.PolicyAttachment` (`tg.AuthorizationPolicy`, `tg.RequestAuthentication`) | `targetRefs` Gateway | `selector` on the server workload | `targetRefs` Service (binds to its waypoint) |
| `.DestV1` / `.DestV2` / `.Dest` / `.DestApp` | shared backend | the feature's backend | the feature's backend |
| `tg.DestinationPolicy(t, trafficPolicy)` | DR on a per-feature ServiceEntry alias, HTTPRoute to it | DR on `.Dest`, GAMMA HTTPRoute to it | same |
| `tg.ServiceEntry(t, spec)` (`.SEHost`) | per-feature ServiceEntry, HTTPRoute (Hostname backendRef) to it | ServiceEntry with a per-feature address the client dials | same, bound to the waypoint (`istio.io/use-waypoint`) |
| `tg.Telemetry(body)`, `tg.Proxy()`, `tg.Metrics`, `tg.Logs`, `tg.WitnessTelemetry` | the gateway | the backend's (inbound) sidecar | the shared waypoint (select series by `tg.DestinationWorkload()`, log lines by a unique marker) |
| `.SourceLabels` / `.SourceNamespace` (who calls the destination) | the gateway pods | the meshed client | the meshed client |

`TargetOptions.OwnClient` (sidecar role) deploys a meshed client for the feature alone, so client-scoped
config (a `Sidecar` resource selecting `.ClientApp`) affects no other feature.
`TargetOptions.Routing` picks how `.TargetHost` is routed: `RouteDefault` (to `.DestV1`, for
policies), `RouteVirtualService` (the feature applies `tg.VirtualService`), `RouteNone` (the feature
routes it, e.g. `tg.DestinationPolicy`). `Shared` reuses the proxy's shared gateway for route-level
config on ingress. Keep a feature ingress-only (`ingressRole`, using `s.NewGateway`/`s.SharedGateway`)
when the behavior is gateway-specific: Istio Gateway servers, listener TLS, several hostnames,
client addresses, XFF/XFCC at the edge. On waypoints, policies never attach by selector: a selector
policy is enforced by ztunnel at L4.

**Install-time settings.** `Feature.MeshConfig` (a MeshConfig YAML patch) and `Feature.IstiodEnv`
(istiod environment variables, i.e. feature flags) are applied for the feature and restored after it.
These features change the whole mesh, so the runner runs them one at a time after the proxy's
parallel features, keeping the `<proxy>/<feature-id>` names, and restarts istiod once per group of
identical `IstiodEnv` (`meshwide_test.go`). The MeshConfig patch is templated like feature config
(`{{.SystemNamespace}}`). Negative controls run them without the patch. Mesh config
has no direct observable: assert a positive effect of the patch with a retrying check before any
negative check (see `features_meshconfig_test.go`).

Rules:

- Use only `Scenario`/`Target`/`Gateway` helpers. Never reference an implementation, resource names
  that differ between implementations, or config dumps/admin endpoints. Attach policy via
  `.PolicyAttachment`, or for ingress-only features `targetRefs` or the
  `gateway.networking.k8s.io/gateway-name` label.
- On workload roles `s.Target` (or the lower-level `s.Workload(t)`) deploys the feature's own backend
  (echo v1+v2 plus per-version Services) and also sets `.ServerSvc`, `.ServerV1`, `.ServerV2`,
  `.ServerApp` and `.ClientPrincipal`. The per-feature backend is what keeps features isolated when
  they run in parallel. Call it before applying config.
- Route-level features share one gateway per proxy (`s.SharedGateway()`) and must use `s.Host()`.
  Anything gateway-wide (policies, listeners, annotations) uses `s.NewGateway`.
- Negative checks ("header absent", "not denied") must use `ExpectStable`, which requires the result to
  hold for a while, so they don't pass before config has propagated.
- `Covers` entries must be string literals (they are read from source).
- **Families.** For an enumeration (connectionPool limits, consistentHash variants, Istio Gateway TLS
  settings, metrics overrides per metric), write one generator returning the feature's `Run` and one
  short `Feature` literal per value with its own ID and `Covers` (`Run: connectionPoolOverflow(policy)`),
  so each value gets its own outcome; see `features_destinationrule_test.go`. Use `s.Case` instead when
  the values share one config object.
- **No `time.Sleep`** in features (enforced). A negative check must follow a positive *witness* of the
  same config object: e.g. a Telemetry object carries `WitnessMetricsOverride(nonce)` and the feature
  calls `gw.WitnessTelemetry(t, nonce)` before checking that something is absent. Use
  `expectNoGatewayLog(t, gw, marker, flushWitness)` for log absence; pass a path requested after the
  marker that is expected in the log, so absence is exact. See `witness_test.go`.
- Probabilistic checks (`Distribution`, `statusShare`) derive their tolerance from the sample size;
  send `Count: SampleSize(weights...)` requests. Prefer deterministic designs (0/100, header steering).
- Timing checks are two-sided: also time a control route without the configured delay.
- Split independent sub-behaviors into `s.Case(t, "<name>", func(t framework.TestContext) {...})`, so an
  implementation supporting some of them is `partial`, not `fail`. Case names are string literals.
- `Profile` defaults to `ProfileCore` (`ProfileImplSpecific` for `impl-specific.*` IDs). When renaming
  a feature, keep the old ID in `Aliases`.
- A new feature needs an entry in the reference manifest of each of its roles: run it against the
  reference with `-istio.test.pcs.update-declared`. Other manifests get `unknown` from
  `coverage -update`.

## Keeping the suite up to date

`coverage/` is a regular unit test (no build tag, no cluster) that runs in `make test` on every PR, as
do `outcome/` and `cmd/pcs/`. `make pcs-check` runs just these; `make pcs-update` regenerates the
generated files and lowers the ratchets after a change to the features or the API.

### Sources → surface

**TestCoverage** computes the surface from these sources (the `source` of each entry in
`coverage/data/surface.yaml`):

- `proto`: every field and enum value of the Istio config kinds in istio/api, via protobuf reflection
  (`<Kind>.<jsonPath>`, `<Kind>.<jsonPath>=<VALUE>`).
- `annotation-registry`, `label-registry`: the istio/api annotation and label registries
  (`annotation:<name>`, `label:<name>`) for Pod, Namespace, Service, ServiceEntry, Gateway and
  WorkloadEntry.
- `feature-flags`: env var flags of the data-plane components, as `flag:<component>:<ENV>`:
  `istiod` (pilot/pkg/features flags referenced from the code that generates proxy config), and
  `agent`, `cni` and `iptables` (every `env.Register` in pilot-agent, istio-cni, or
  istio-iptables/istio-nftables and the packages they import, except packages of another component).
- `extension-registry`: Istio's Gateway API extensions, from `gatewayext.Registry()` in
  `pilot/pkg/config/kube/gatewayext`, next to the translators that implement them
  (`gateway-api-ext:<...>`). **TestExtensionKeysTracked** scans the translators
  (`pilot/pkg/config/kube/{gateway,gatewaycommon,agentgateway}`) for Istio-specific string keys
  (`*.istio.io/...`), so a key added there without a registry entry fails.
- `manual`: `coverage/data/interactions.yaml`, the reviewed inventory of multi-resource semantics
  (`interaction:<id>`, e.g. authz evaluation order or DestinationRule subset precedence). This is
  the only hand-maintained source. An interaction is also covered when its `coveredBy` names existing
  features.

`coverage/data/surface.yaml` maps each entry to its source and changes only with the API;
`coverage/data/coverage.yaml` resolves each entry against the features and the decisions below and
changes with them. Exempt entries are one line there (`{exempt: true}`, or `{exempt: <prefix>}` for a
`*` exemption).

The decisions about the surface are checked in, one file per concern, in `coverage/data/`:

- `coverage/data/exemptions.yaml`: surface deliberately not covered, each with a real reason.
- `coverage/data/scope.yaml`: which roles must cover which surface with a feature of their own (e.g.
  sidecar and waypoint: AuthorizationPolicy, DestinationRule, ...).
- `coverage/data/ratchets.yaml`: `maxTodo` (entries neither covered nor exempt) and `maxRoleGaps` (per
  role, in-scope entries no feature of the role covers). They may only go down; `-update -raise-todo`
  raises them for a deliberate surface expansion (e.g. tracking a new API).

TestCoverage fails on new surface nobody decided about (e.g. a new AuthorizationPolicy field after an
istio/api bump) beyond the ratchets, `Covers` entries that don't exist (typos, removed fields), stale
or redundant exemptions, a feature with no or unknown `Roles`, a role with no features, and
out-of-date generated views: `coverage/data/surface.yaml`, `coverage/data/coverage.yaml`,
`coverage/data/catalog.yaml` (feature metadata: id,
description, roles, profile, covers, aliases, cases; tooling and docs read it instead of Go source)
and `coverage/data/summary.yaml` (the numbers).

**Defaults.** `coverage/defaults/` walks the proxy config istiod generates by default and maps each
default to a feature or a reason (`defaults-inventory.yaml`). `defaults-values.yaml` records the values
of those defaults, so a changed default (it changes behavior for users of every proxy) fails until the
features observing it are reviewed and the file regenerated.

**Release notes and translator goldens.** These catch proxy-facing changes that add no new surface:

- **TestReleaseNotesTracked** requires a changed release note in a proxy area (`traffic-management`,
  `security`, `networking`, `network`, `telemetry`, `extensibility`, `EnvoyFilter`, `pilot`) to
  record the coverage decision in the note: `# pcs: feature <id>` (this PR adds it),
  `# pcs: covered-by <id>` (already covered) or `# pcs: not-proxy-behavior: <reason>`. The referenced
  feature must exist. Notes the PR does not touch need no decision.
- **TestReleaseNoteBacklog** requires every proxy-area note in the repository to carry a `# pcs:`
  decision or be triaged in `coverage/data/releasenote-backlog.yaml` (notes written before the gate). The
  backlog uses the same decisions plus `todo`, ratcheted by `maxBacklogTodo`; `-update` drops entries
  for removed notes and adds missing ones (auto-triaged only for test/docs kinds and purely
  control-plane text). `make pcs.backlog PCS_ARGS="-from <release tag>"` lists the untriaged notes
  of a release as a checklist.
- **TestTranslatorGoldensTracked** treats changed translator test data
  (`pilot/pkg/config/kube/{gateway,gatewaycommon,ingress}/testdata`) as evidence of a proxy-facing
  change: the PR needs a changed proxy-area release note with a `# pcs:` decision, or a commit message
  line `pcs: feature <id>`, `pcs: covered-by <id>` or `pcs: not-proxy-behavior: <reason>`.

### Tests

- **TestFeatureFiles** keeps each feature in the file for its area (first ID segment).
- **TestNoSleepsInFeatures** rejects `time.Sleep` in `features_*_test.go`.
- At run time, a passing feature must have set every key of an applied kind it lists in `Covers`
  (see [Outcomes](#outcomes-declarations-and-reports)).

### Report

**TestDeclaredSupport** requires every feature of a role to have an entry in each
`declared/<role>/<impl>.yaml`, no stale entries, and a reason for every gap. `unknown` entries are
ratcheted (`maxUnknown`), and reference manifests cannot gain new ones.

After adding features, run `make pcs-update` to drop exemptions that are now covered, regenerate
`coverage/data/catalog.yaml`, `surface.yaml`, `coverage.yaml` and `summary.yaml`, add `unknown` manifest entries for
non-reference implementations, and lower the ratchets (`maxTodo`, `maxRoleGaps`, `maxUnknown`). Then
run the new features against the reference with `-istio.test.pcs.update-declared` (`make pcs.changed`
prints the `-run` expression).

To browse the declared support and the API surface coverage, generate a self-contained HTML page
(one tab per role, features grouped by area, proxies as columns):

```bash
go run ./tests/conformance/util -out conformance.html
```

The API filter selects features by the kinds they apply, as recorded in the reference manifest
(`kinds`, written by the last reference run with `-istio.test.pcs.update-declared`, scaffolding such
as Services and Secrets included). E.g. "Gateway API / Kubernetes only" lists the features that use
no Istio API (CRDs, MeshConfig, istiod flags), the surface a Gateway API implementation can run.
Config the harness applies for every feature of a role is not recorded: the shared gateway
(ingress, waypoint, egress) and the waypoint's `NoConnectionReuse` DestinationRule.
