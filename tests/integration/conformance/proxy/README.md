# Istio Proxy Conformance Suite (PCS)

Black-box tests that run one set of implementation-neutral features against every proxy Istio
supports in a role, to produce definitive data on what each implementation supports. Design:
`architecture/tests/proxy-conformance.md`.

Currently covered:

- **ingress gateway** (north-south): Envoy (`istio` GatewayClass) and agentgateway
  (`istio-agentgateway`).
- **egress gateway**: Envoy, reached through Istio API config (`Gateway` with
  `selector: istio: egressgateway`). The suite skips when the installation has no egress gateway.
  The egress feature backlog is `architecture/tests/egress-use-cases.md`.
- **sidecar**: Envoy, client and server sidecar-injected.
- **node**: ztunnel, the ambient L4 proxy.
- **waypoint**: Envoy and agentgateway waypoints, through a waypoint attached to the destination
  service (ztunnel is present, as in production).

Each role is its own test (`Test<Role>Conformance`) and each feature declares the roles it applies
to. There is no combinatorial "ambient vs sidecar" axis: ambient is node + waypoint, and both are
covered by their own roles. Mode parity and mixed-mode interop are separate, smaller suites, still
to do — see `architecture/tests/proxy-conformance.md` §3.9.

## Scope

Only **Istio-specific** behavior is tested here:

- Istio APIs on gateways: AuthorizationPolicy, RequestAuthentication, PeerAuthentication,
  DestinationRule, ServiceEntry, Telemetry, Istio Gateway/VirtualService, ProxyConfig `gatewayTopology`.
- Istio extensions of Gateway API: annotations, listener `tls.options`, `Hostname` backendRefs,
  parametersRef ConfigMap overlays, manual deployment, the deployment controller.
- Istio data-plane defaults: XFF/XFCC handling, request ids, retries, metrics, access logs.

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
go test -tags=integ -run TestIngressConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestEgressConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestSidecarConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestNodeConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestWaypointConformance ./tests/integration/conformance/proxy/

# one proxy, one area
go test -tags=integ -run 'TestIngressConformance/agentgateway/security' ./tests/integration/conformance/proxy/ \
  -istio.test.pcs.proxies=agentgateway

# upstream Gateway API conformance, per proxy (slow)
go test -tags=integ -run TestGatewayAPIConformance ./tests/integration/conformance/proxy/
```

The node and waypoint suites (and any ambient path) need an ambient installation; they skip when
the suite runs against a cluster without ztunnel.

Subtests are named `<proxy>/<feature-id>`, with the runs used for classification below them
(`run`, and on failure `rerun` and `control`).

### Outcomes, declarations and reports

Each result is classified as `supported`, `partial`, `rejected`, `fail-closed`, `fail` or `ignored`
(or `flaky`/`indeterminate`, which are test problems), and compared with the proxy's declared support
in `declared/<role>/<impl>.yaml`:

- observed matches the declaration: **pass** (supported) or **skip** (known gap);
- worse than declared: **fail** (regression);
- better than declared: **fail** until the manifest is updated;
- flaky, indeterminate, or a security feature silently ignored without a declared `ignored` + issue:
  **fail** regardless;
- `unknown`: skipped and reported.

So the suite is green for an implementation that is exactly as conformant as it declares. Flags:

```sh
-istio.test.pcs.update-declared   # rewrite declared/<role>/<impl>.yaml from this run; review the diff
-istio.test.pcs.classify=false    # no rerun/control on failure (faster, failures classified as fail/rejected)
-istio.test.pcs.report-dir=DIR    # where pcs-report-<role>.yaml goes (default $ARTIFACTS)
```

The report (`pcs-report-<role>.yaml`) lists per implementation a per-profile summary and per feature
the outcome, reason, failed assertion (file:line), cases, declaration and decision. See
`architecture/tests/proxy-conformance.md` §3.6–3.7.

## Writing a feature

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
identical `IstiodEnv` (`meshwide_test.go`). Negative controls run them without the patch. Mesh config
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

`coverage/` is a regular unit test (no build tag, no cluster) that runs in `make test` on every PR:

- **TestCoverage** computes the API surface from the istio/api protos (via protobuf reflection), the
  istio/api annotation and label registries (`annotation:<name>`, `label:<name>`, for Pod, Namespace,
  Service, ServiceEntry, Gateway and WorkloadEntry), istiod feature flags referenced from the code that
  generates proxy config (`flag:<ENV>`), a list of Istio's Gateway API extensions, and the reviewed
  inventory of multi-resource semantics in `coverage/interactions.yaml` (`interaction:<id>`, e.g.
  authz evaluation order or DestinationRule subset precedence), and compares it with the features'
  `Covers` declarations plus `coverage/exemptions.yaml`. An interaction is also covered when its
  `coveredBy` names existing features. It fails on:
  - new surface nobody decided about (e.g. a new AuthorizationPolicy field after an istio/api bump),
  - `Covers` entries that don't exist (typos, removed fields),
  - stale or redundant exemptions,
  - growth of `todo` exemptions beyond `maxTodo` (a ratchet: the backlog can only shrink).
- **TestExtensionKeysTracked** scans the Gateway API translators
  (`pilot/pkg/config/kube/{gateway,gatewaycommon,agentgateway}`) for Istio-specific string keys
  (`*.istio.io/...`), so a new annotation or `tls.options` key added in istiod fails the check until
  it is tracked.
- **TestReleaseNotesTracked** requires a changed release note in a proxy area (`traffic-management`,
  `security`, `networking`, `network`, `telemetry`, `extensibility`, `EnvoyFilter`, `pilot`) to
  record the coverage decision in the note: `# pcs: feature <id>` (this PR adds it),
  `# pcs: covered-by <id>` (already covered) or `# pcs: not-proxy-behavior: <reason>`. The referenced
  feature must exist. Notes the PR does not touch need no decision.
- **TestReleaseNoteBacklog** requires every proxy-area note in the repository to carry a `# pcs:`
  decision or be triaged in `coverage/releasenote-backlog.yaml` (notes written before the gate). The
  backlog uses the same decisions plus `todo`, ratcheted by `maxBacklogTodo`; `-update` drops entries
  for removed notes and adds missing ones (auto-triaged only for test/docs kinds and purely
  control-plane text).
- Every feature must list the roles it applies to; the check fails on a feature with no `Roles`, an
  unknown role, or a declared role with no features, and reports coverage per role.

- **TestCoverage** also ratchets coverage **per role**: `roleScope` in `exemptions.yaml` lists the
  surface each non-ingress role must cover itself (e.g. sidecar and waypoint: AuthorizationPolicy,
  DestinationRule, ...), and `maxRoleGaps` may only go down.
- **TestCoverage** keeps `catalog.yaml` (generated feature metadata: id, description, roles, profile,
  covers, aliases, cases) in sync; tooling and docs read it instead of Go source.
- **TestDeclaredSupport** requires every feature of a role to have an entry in each
  `declared/<role>/<impl>.yaml`, no stale entries, a reason for every gap and an issue for an ignored
  security feature. `unknown` entries are ratcheted (`maxUnknown`), and reference manifests cannot
  gain new ones.
- **TestTranslatorGoldensTracked** treats changed translator test data
  (`pilot/pkg/config/kube/{gateway,gatewaycommon,ingress}/testdata`) as evidence of a proxy-facing
  change: the PR needs a changed proxy-area release note with a `# pcs:` decision, or a commit message
  line `pcs: feature <id>`, `pcs: covered-by <id>` or `pcs: not-proxy-behavior: <reason>`.
- **TestNoSleepsInFeatures** rejects `time.Sleep` in `features_*_test.go`.

After adding features, run `go test ./tests/integration/conformance/proxy/coverage/ -run TestCoverage -update`
to drop entries that are now covered, regenerate `catalog.yaml`, add `unknown` manifest entries for
non-reference implementations, and lower the ratchets (`maxTodo`, `maxRoleGaps`, `maxUnknown`).
