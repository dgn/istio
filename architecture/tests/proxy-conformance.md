# Proxy-Agnostic Istio: Conformance, Visibility, Convergence

Status: Draft / Proposal
Branch: `proxy-conformance`

## TL;DR

Istio was designed around Envoy as its data plane proxy, and that relationship is deeply embedded in
how istiod operates today. With the introduction of ztunnel, we reached a first inflection point
where Envoy was no longer the only supported data plane. That already introduced structural issues
around feature parity and our ability to track and maintain consistent behavior across proxies. Now,
with the addition of agentgateway, we are stretching our existing code base even further by
duplicating config generation logic to work with the respective agentgateway abstractions. This
document proposes a structured approach to dealing with the challenges of three supported proxies,
and ultimately aims to set up the Istio project to more easily adopt new proxies in the future, with
less toil and more confidence for maintainers and users that existing behaviors are preserved.

## 1. Problem

### 1.1 Where we are

Istio ships three data plane proxies across five roles:

| Role                 | Implementations today      |
|----------------------|----------------------------|
| Sidecar              | Envoy                      |
| Waypoint (L7)        | Envoy, agentgateway (experimental, `istio-agentgateway-waypoint`) |
| Node / L4 (ambient)  | ztunnel                    |
| Ingress gateway      | Envoy (`istio` class), agentgateway (`istio-agentgateway` class) |
| Egress gateway       | Envoy                      |

istiod generates config for them through separate pipelines:

- **Envoy:** Gateway API is first converted to Istio APIs (`pilot/pkg/config/kube/gateway`, about 7k
  lines of non-test code). About 23k lines in `pilot/pkg/networking` plus 2.8k in
  `pilot/pkg/security/authz` then lower `VirtualService`, `DestinationRule`, `AuthorizationPolicy`,
  `Sidecar`, `Telemetry` and `EnvoyFilter` into Envoy xDS. The Istio APIs are, in effect, the
  intermediate format for Envoy, with Envoy semantics baked in.
- **ztunnel:** the workload API (WDS), a proxy-neutral wire format.
- **agentgateway:** `pilot/pkg/config/kube/agentgateway` (about 6.9k lines) translates Gateway API
  directly into the agentgateway API. It duplicates the Gateway API translation the Envoy path
  already has, and it reads none of the Istio policy APIs.

### 1.2 What breaks

- **No source of truth for parity.** Nobody can say which Istio features work on which proxy in which
  role. Feature tests are spread across about 5k lines of `tests/integration/pilot/common/routing.go`,
  `tests/integration/ambient/*` and Gateway API conformance runs, and many assertions read Envoy
  internals (e.g. `hasHTTP2Keepalive` parses an Envoy config dump), so they can't run against another
  proxy.
- **Unsupported config is silently ignored.** istiod accepts config that the target proxy never
  receives. The PoC (§4) measured the worst case: an `AuthorizationPolicy` DENY bound to an
  agentgateway gateway is accepted with no status signal and **not enforced**; traffic passes.
- **Drift is structural.** Every feature added to one pipeline has to be added to the others by hand,
  and nothing fails when that doesn't happen.
- **New proxies are expensive.** Adding a proxy today means another translation pipeline and another
  set of Envoy-coupled tests, with no way to know when it is "done".

## 2. Approach

Three steps, each useful on its own and each a prerequisite for the next:

| Step | What | Answers |
|---|---|---|
| **1. Measure** | A black-box, implementation-neutral **Proxy Conformance Suite (PCS)**, plus a deterministic **coverage check** on every PR (§3) | Which features work where? Is new behavior tested? |
| **2. Make gaps visible** | A **capability and status layer** in istiod: config a proxy can't honor is reported, and security config fails closed (§5) | Do users learn that their config does nothing? |
| **3. Converge** | A **shared intermediate representation** in istiod that every proxy lowers from (§6) | Can parity hold by construction instead of by effort? |

Step 1 produces the data that justifies steps 2 and 3, and it is the safety net that makes step 3 a
refactor rather than a rewrite.

**Goals**
1. A definitive, machine-checkable answer to "does feature X work on proxy Y in role Z".
2. No silent failures: unsupported config is visible, and unsupported *security* config fails closed.
3. Behavior is preserved: changes to istiod, Istio APIs or a proxy cannot silently change or drop
   tested behavior.
4. Adding a proxy is a bounded, checkable amount of work (§7).
5. Reuse the existing test framework (`pkg/test/framework`, echo, `check`).

**Non-goals**
- Performance comparison between proxies (a separate benchmark suite could reuse the harness).
- Making Envoy-only extension APIs (`EnvoyFilter`, `WasmPlugin`, Lua/Wasm `TrafficExtension`) portable.
  They are catalogued as `impl-specific` so the matrix shows what is lost by switching, but they are
  not conformance requirements.
- Replacing upstream Gateway API conformance; PCS runs it per proxy instead (§3.3).

## 3. Step 1: Measure

### 3.1 Model

```
Feature  ×  Proxy(implementation, role)  →  result
```

- **Proxy:** an implementation in a role, e.g. `envoy` and `agentgateway` as gateways. Role and
  implementation are separate axes throughout (the production model mixes them; see §9.1).
- **Feature:** one externally observable behavior, with a stable hierarchical ID
  (`security.authz.deny.path`), a description, the roles it applies to, and the API surface it
  `Covers` (used by the coverage check, §3.5). A feature is written once against a `Scenario`
  abstraction that hides everything implementation-specific: how a gateway is deployed, how policy
  attaches (`targetRefs` or the standard `gateway.networking.k8s.io/gateway-name` label), how
  traffic is sent.
- **Result:** pass or fail per (proxy, feature). Subtests are named `<proxy>/<feature-id>`. The suite
  is expected to be red for implementations that are not feature-complete; that red is the data.

### 3.2 Black-box rules

Assertions may only use the echo backend's view of the request (status, headers, path, protocol,
SNI, which backend version answered), Kubernetes status, the proxy's standard telemetry
(`:15020/stats/prometheus`, access logs), and timing. Config dumps, admin endpoints and `istioctl
proxy-config` are not allowed. If a behavior can only be seen that way, either echo is improved or
the behavior is not conformance-relevant.

The PoC added rules that are easy to get wrong:

- **Observe at an out-of-mesh backend when the header or protocol matters.** An in-mesh backend's
  sidecar rewrites `X-Forwarded-Client-Cert`, adds `X-Request-Id`, and re-encodes HTTP/2 to the app.
  A test that observes behind a sidecar measures the sidecar, not the proxy under test.
- **Keep the effect at the proxy under test.** Namespace-wide and mesh-wide policies also apply to an
  in-mesh backend's sidecar, which then enforces them instead.
- **Avoid paths that hide the client.** A LoadBalancer path SNATs the client address; client-IP
  features call the gateway pod directly.
- **Test product defaults, not test-environment defaults.** `tests/integration/base.yaml` disables
  retries mesh-wide for all integration tests; PCS restores Istio's built-in default.

### 3.3 Scope

PCS covers **Istio-specific** behavior only:

- Istio APIs on the proxy: AuthorizationPolicy, RequestAuthentication, PeerAuthentication,
  DestinationRule, ServiceEntry, Telemetry, Istio Gateway/VirtualService, ProxyConfig.
- Istio extensions of Gateway API: annotations, listener `tls.options`, `Hostname` backendRefs,
  parametersRef overlays, manual deployment, the deployment controller.
- Istio data plane defaults: XFF/XFCC handling, request IDs, retries, metrics, access logs.

Plain Gateway API semantics are covered by upstream conformance, which `TestGatewayAPIConformance`
runs against every proxy with **all** gateway features enabled, so a failing upstream test means
"not supported by that proxy" rather than "not claimed".

### 3.4 Test validity: negative controls

A test is only valid if it passes on the reference implementation **and fails when the feature is
missing**. The PoC found tests that passed on agentgateway while it ignored the feature entirely, e.g.
because a backend sidecar enforced the policy, or because "header disabled" passes trivially for a
proxy that never sets the header.

`TestIngressConformanceControls` (ingress), `TestEgressConformanceControls` (egress),
`TestSidecarConformanceControls` and `TestWaypointConformanceControls` check this systematically. Each feature runs against the role's
reference implementation with its config under test removed: Istio policy and routing objects and
ConfigMaps are stripped from what the feature applies, as are the gateway's annotations, labels,
`infrastructure`, `addresses` and `tls`, and the feature's MeshConfig patch and istiod environment.
Scaffolding (routes, Services, ServiceEntries, Istio Gateways, Secrets) stays. If every assertion
still passes, the test is **weak** and the control fails. Features with nothing to strip are not
applicable; features with no client-visible effect (e.g. AUDIT) declare `NoControl` with the reason.

### 3.5 Keeping it current: the coverage check

`tests/integration/conformance/proxy/coverage/` is a plain unit test (no cluster, no build tag), so it
runs in `make test` on every PR. It computes the **API surface** the suite must cover:

- every field and enum value of the in-scope Istio APIs, by protobuf reflection;
- `MeshConfig` and `ProxyConfig` in full, with sidecar-only and istiod-internal subtrees exempted by
  prefix with a reason;
- the Istio APIs of every role: AuthorizationPolicy, RequestAuthentication, PeerAuthentication,
  Gateway, VirtualService, DestinationRule, ServiceEntry, Sidecar, WorkloadEntry, WorkloadGroup,
  Telemetry and WasmPlugin (whose API has no Envoy-isms); EnvoyFilter is tracked but exempted as a
  whole (raw Envoy patches, impl-specific features);
- Istio annotations and labels from the istio/api registries that apply to Pod, Namespace, Service,
  ServiceEntry, Gateway or WorkloadEntry (installation and bookkeeping keys exempted with a reason),
  plus Istio's Gateway API extensions;
- every istiod feature flag (`pilot/pkg/features`) referenced from code that generates proxy config
  (Gateway API translators, `pilot/pkg/networking/...`, `pilot/pkg/model`, `pilot/pkg/xds/...`,
  `pilot/pkg/security/...`, the ambient workload API in `pilot/pkg/serviceregistry/ambient`), with
  control-plane-only flags (debouncing, caches, debug endpoints) exempted;
- multi-resource semantics that are not single fields, from the reviewed inventory
  `coverage/interactions.yaml` (`interaction:<id>`: authz evaluation order, policy scope precedence,
  DestinationRule merging, VirtualService delegation, exportTo, protocol selection, ...). Each entry
  names its roles (an implicit `roleScope`) and a source; features cover it through `Covers`, or the
  entry lists existing features in `coveredBy`.

Subtrees that repeat a type tracked elsewhere (`DestinationRule.subsets.trafficPolicy`,
`portLevelSettings`, `MeshConfig.defaultTrafficPolicy`) collapse to one entry, since only their
precedence semantics need their own test.

Each surface entry must be covered by a feature's `Covers` list, or exempted in `exemptions.yaml`
with a reason. The check fails when:

- new surface appears (a field added in istio/api, a new annotation, a new flag on a gateway code path);
- a `Covers` entry doesn't exist (typo, removed field);
- an exemption is stale or redundant;
- the number of `todo` exemptions grows. `maxTodo` is a ratchet that only goes down; deliberately
  widening the surface requires `-raise-todo`, which shows up in review.

A companion test, `TestExtensionKeysTracked`, scans the Gateway API translators for `*.istio.io/...`
string keys, so a new annotation or `tls.options` key in istiod cannot be added untracked.

Every feature must name the roles it applies to. The check parses `Roles` (the `Role` constants and
aliases such as `ingressRole`), fails when a feature lists none or an unknown role, fails when a
declared role has no features at all, and reports features and covered entries per role, e.g.
`egress: 2 features, 19 entries`. This puts the requirement in `make test`, not only in the
integration runner's registration panic.

`TestReleaseNotesTracked` uses release notes as Istio's own declaration that a change is user-facing.
Every changed note in a proxy area (`traffic-management`, `security`, `networking`, `network`,
`telemetry`, `extensibility`, `EnvoyFilter`, `pilot`) records the coverage decision in the note
itself:

```yaml
# pcs: feature <feature-id>        this PR adds that conformance feature
# pcs: covered-by <feature-id>     the behavior is already covered by that feature
# pcs: not-proxy-behavior: <why>   the change cannot affect a proxy (reason required)
```

The check validates that the referenced feature exists and that `not-proxy-behavior` gives a reason.
The decision is reviewable where the claim is made, cannot be satisfied by unrelated file churn, and
is not demanded for notes the PR does not touch. The base commit is
`PULL_BASE_SHA`/`GITHUB_BASE_SHA`, else the closest merge base with `master` or `origin/master`;
without git the check skips. Notes outside the proxy areas (installation, istioctl, documentation)
are not gated, so a proxy-behavior change filed in one of those areas would be missed.

Notes that predate the gate are triaged in `coverage/releasenote-backlog.yaml`
(`TestReleaseNoteBacklog`, no git needed): every proxy-area note must carry a `# pcs:` decision or
have a backlog entry with one (or `todo`, ratcheted by `maxBacklogTodo`). `-update` pre-classifies
only notes of a test/docs kind or whose text matches only control-plane topics (istioctl, Helm
values, installation) and none of a broad list of proxy terms; everything else starts as `todo`.

**Per-role coverage.** The global check counts an entry as covered when any role covers it, so a new
role could stay almost empty without moving the ratchet (the egress role's first two features only
registered `Gateway.servers.port.number`, the rest was already covered by ingress). `roleScope` in
`exemptions.yaml` therefore assigns surface subtrees to the non-ingress roles that must cover them
themselves (longest prefix wins; e.g. sidecar and waypoint get AuthorizationPolicy, RequestAuthentication,
DestinationRule, `VirtualService.http`, Telemetry, ServiceEntry; node gets PeerAuthentication and the L4
AuthorizationPolicy fields; egress gets ServiceEntry, `DestinationRule.trafficPolicy.tls`, Istio Gateway
servers and `VirtualService.tls`/`tcp`). `maxRoleGaps` ratchets the uncovered in-scope entries per role.
Entries exempted with a real reason are out of scope for every role. Keys ending in `*` match by
plain prefix (`annotation:sidecar.istio.io/*`). Behavior that is not an API field is tracked in
`coverage/interactions.yaml`; the use-case inventories, like `architecture/tests/egress-use-cases.md`,
remain the source for role-specific scenarios.

**Translator goldens.** Release notes are voluntary. The Gateway API translator test data
(`pilot/pkg/config/kube/{gateway,gatewaycommon,ingress}/testdata`) is deterministic evidence that
proxy-facing output changed, so `TestTranslatorGoldensTracked` requires a PR that changes it to carry a
coverage decision: a changed proxy-area release note with a `# pcs:` line, or the same decision as a
commit message line (`pcs: covered-by <id>`, ...). It complements the release-note gate; it does not
see changes to xDS generation, which has no goldens.

### 3.6 Detecting behavior deterministically

A result is only data if rerunning the suite gives the same answer. The PoC had three sources of
nondeterminism, each now handled structurally rather than per test.

**Propagation witnesses instead of sleeps.** Config reaches the proxy asynchronously. A positive check
("returns 403") synchronizes itself: `Expect` retries until it holds. A negative check ("no access log
line", "counter did not increase") holds trivially before the config arrives, so the PoC slept 10s and
hoped. Negative checks now come after a *witness*: a positive, observable effect carried by the same
config object, which the proxy receives in the same push. For Telemetry, which has no client-visible
effect, the witness is a `tagOverrides` label `pcs_witness=<nonce>` added to
`istio_request_duration_milliseconds` in the same object (`WitnessMetricsOverride`,
`Gateway.WitnessTelemetry`). Ordering gives exact absence checks where a positive exists in the same
stream: `telemetry.accesslog.filter` sends the unlogged request first and the logged one second, so once
the second is logged, the first would have been. Where no positive exists, absence is concluded after a
documented latency bound (`logFlushBound`, Envoy's file flush interval plus margin), and only for traffic
sent after the witness. Features may not call `time.Sleep`; the coverage check enforces it.
`ExpectStable` remains valid for negatives whose baseline is the opposite (the header is present without
the config), which the negative controls verify.

**Statistics derived, not picked.** Weighted routing and fault percentages are probabilistic. A
hand-picked `+/- 12` on 100 requests is a ~3σ bound, a false failure every ~370 runs, multiplied by
hundreds of (proxy, feature) pairs. `outcome.CheckShare` derives the tolerance from the sample size and
a false-failure rate of 1e-6 per share (z = 4.89), and `SampleSize` picks the request count that keeps the
tolerance within 10 points; a check with too few samples fails as such. Deterministic designs (0/100
then 100/0, header steering) are preferred where the behavior allows.

**Timing is two-sided.** A delay feature also times a control route of the same object in the same
attempt: the delayed route must exceed the delay and the control must stay well below it, so a slow
environment cannot pass the lower bound with no delay configured.

### 3.7 Outcomes and declared support

The harness, not the feature author, classifies results. A feature's `Run` stays imperative, but it runs
against a recorder instead of the real test, so a failed assertion becomes evidence rather than a
verdict (`classify_test.go`, logic in the cluster-free `outcome/` package):

| Outcome | Meaning | Detected by |
|---|---|---|
| `supported` | All behavioral checks pass. | |
| `partial` | Some subcases pass, e.g. methods yes, `when` headers no. | `Scenario.Case` results |
| `rejected` | Config visibly refused. | Admission error, or a False `Accepted`/`ResolvedRefs`/`Programmed`/`Reconciled` condition (incl. route `parents` and policy `ancestors`) on an applied object, read before cleanup |
| `fail-closed` | Accepted silently, traffic denied (security features only). | The failing call was answered 401/403 or reset |
| `ignored` | Accepted, no status, behavior unchanged. | A negative control on the *same* proxy (config removed) fails at the same assertion (file:line) |
| `fail` | Wrong behavior of another kind. | Otherwise |
| `indeterminate` | Propagation could not be established (witness never appeared). Not a result. | `Scenario.Indeterminate` |
| `flaky` | Failed, then passed on rerun. Not a result; the test must be fixed. | Rerun |

A failed feature therefore costs up to two more runs (rerun, control), each in its own subtest
(`<proxy>/<feature>/{run,rerun,control}`) so resources are cleaned up between them;
`-istio.test.pcs.classify=false` skips both. The per-proxy control also makes "Ignored" a runtime
measurement for every implementation, where the negative controls (§3.4) only validate tests against
the reference.

**Declared support.** Each (role, implementation) has a checked-in manifest,
`declared/<role>/<impl>.yaml`, one of which per role is the reference. Every feature of the role has an
entry: a status (an outcome, `unsupported` for any gap, or `unknown`), a reason for gaps, an issue for an
ignored security feature, and the passing cases for `partial`. The harness compares observed with
declared (`outcome.Decide`):

- matches a declared `supported`: pass;
- matches a declared gap: the subtest is **skipped** as a known gap, so the suite is green for an
  implementation that is exactly as conformant as it claims, and CI can gate on it;
- worse than declared (outcomes are ranked supported > partial > rejected > fail-closed > fail >
  ignored), or a declared passing case now fails: **regression**, fails;
- better than declared: fails until the manifest is updated, so declarations cannot rot;
- `flaky`, `indeterminate`, or an ignored security feature not declared `ignored` with an issue: fails
  regardless of the declaration;
- `unknown`: skipped and reported. `unknown` entries are ratcheted (`maxUnknown`), and the reference
  manifest cannot gain new ones, so every new feature is declared against the reference when it is added.

`-istio.test.pcs.update-declared` rewrites the manifests from a run (keeping reasons and issues of
unchanged entries) for review. The cluster-free `TestDeclaredSupport` checks that every manifest is
complete, has no stale entries, and has reasons where required.

**Reports.** Every run writes `pcs-report-<role>.yaml` (`$ARTIFACTS` by default), modeled on the Gateway
API `ConformanceReport`: per implementation, a per-profile outcome summary and per feature the outcome,
reason, failed assertion, cases, declaration, decision, attempts and duration. Reports are CI artifacts,
not checked in per PR; per release they are published under `reports/<istio-version>/` and render the
istio.io matrix, keyed by feature ID with `/case` suffixes and mapped to API fields through `Covers`.

**Profiles.** Features belong to `core` (default), `extended`, or `impl-specific` (default for
`impl-specific.*` IDs), so "agentgateway supports waypoint core" is one checkable claim.

**Catalog.** `catalog.yaml` is generated from the feature definitions (`TestCoverage -update`) and
checked for freshness, so documentation and tooling read feature metadata without parsing Go. Renamed
features keep their former IDs in `Aliases`.

### 3.8 Layout and running

```
tests/integration/conformance/proxy/
  main_test.go                 # TestMain: Istio with ambient (ztunnel+CNI) and agentgateway; defaults
  harness_test.go, kube_test.go, checks_test.go, certs_test.go, env_test.go
  proxies_test.go              # proxy list per role; --istio.test.pcs.proxies=envoy,agentgateway
  roles_test.go                # Test<Role>Conformance: proxy × feature, one test per role (§3.9)
  gateway_test.go              # TestIngressConformance (ingress gateways), TestEgressConformance
  workload_test.go             # mesh target: meshed client, per-feature backend, waypoint attachment
  target_test.go               # role-neutral Target: traffic, routing and policy attachment per role
  meshwide_test.go             # Feature.MeshConfig / IstiodEnv: serial phase per proxy, istiod restarts
  controls_test.go             # negative controls (§3.4), ingress, egress and sidecar roles
  classify_test.go             # outcome classification, declared-vs-observed, reports (§3.7)
  witness_test.go              # propagation witnesses and bounded absence checks (§3.6)
  outcome/                     # cluster-free: outcome taxonomy, Decide, statistics, report schema
  declared/<role>/<impl>.yaml  # declared support manifests (§3.7)
  catalog.yaml                 # generated feature catalog
  gatewayapi_test.go           # TestGatewayAPIConformance: upstream suite per proxy, all features
  features_*_test.go           # features by area
  coverage/                    # coverage check (§3.5), runs in unit tests
```

```sh
# one role, all implementations
go test -tags=integ -run TestIngressConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestEgressConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestWaypointConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestNodeConformance ./tests/integration/conformance/proxy/
go test -tags=integ -run TestSidecarConformance ./tests/integration/conformance/proxy/
```

### 3.9 Beyond gateways

Every legitimate proxy role is its own conformance test and its own row of the matrix:

| Role | Topology | Policy attachment |
|---|---|---|
| ingress gateway | client(out-of-mesh) → gateway → backend | `targetRefs`, `gateway-name` label |
| sidecar | client(sidecar) → server(sidecar) | `selector` |
| node (ztunnel) | client(ambient) → server(ambient), **L4 only** | `selector` |
| waypoint | client(ambient) → ztunnel → waypoint → server | `targetRefs` Gateway/Service |
| egress gateway | client(sidecar) → egress gateway → external | `selector` (`istio=egressgateway`) |

Ingress and egress are separate roles even though both are gateways: ingress is a Gateway API
gateway (one `GatewayClass` per implementation), while egress is an istio-proxy deployment the mesh
routes to through Istio API config (a `Gateway` with `selector: istio: egressgateway`, a
`VirtualService` attached to `[mesh, <gateway>]`, and a `ServiceEntry` for the external host).
Implementations differ: agentgateway implements ingress but not the egress gateway. An egress
feature routes its own external host, so features can share one egress gateway without interfering;
it proves the request traversed the gateway with a header the gateway's route leg adds (the negative
control strips the `VirtualService`, so traffic leaves the mesh directly and the header is absent).
The use cases the egress role must cover are inventoried in `architecture/tests/egress-use-cases.md`.

**Role-neutral features.** Most Istio API features mean the same thing on every L7 proxy, so they are
written once against a `Target` and tagged with every such role (`l7Roles`: ingress, sidecar,
waypoint). The Target hides the per-role differences: where traffic enters (gateway address, or the
feature's backend Service intercepted by the client-side proxy), how "this feature's host" is routed
(VirtualService bound to an Istio Gateway or an HTTPRoute on ingress; a mesh VirtualService or an
HTTPRoute with a Service parentRef otherwise), and how policy attaches, following Istio's rules:
`targetRefs` Gateway on ingress, a `selector` on the server workload for sidecars (server-side policy
is enforced by the server's inbound proxy) and the node proxy, `targetRefs` Service for waypoints
(selector policies never bind to a waypoint; ztunnel would enforce them at L4). Features stay
ingress-only where the behavior is gateway-specific: Istio Gateway servers, listener TLS, multiple
hostnames, client addresses, XFF/XFCC at the edge.

**Install-time settings.** `Feature.MeshConfig` and `Feature.IstiodEnv` cover MeshConfig fields and
istiod feature flags. They change the whole mesh, so each such feature runs alone, after the proxy's
parallel features (a gate holds them until no parallel feature has run for a few seconds, which keeps
the `<proxy>/<feature-id>` subtest names; Go runs parallel subtests only after the parent returns and
forbids `t.Run` in cleanups). The patch is applied per attempt and restored after it; istiod is
restarted once per group of identical environments and restored when the proxy is done. Mesh config
has no direct observable, so each feature pairs its patch with a positive effect checked by a
retrying assertion.

Mesh features (`mesh.*`) are tagged with the concrete roles they exercise: L4 behavior
(reachability, identity-based authz, mTLS) is tagged `node` + `sidecar`; L7 behavior (routing,
path/header authz, fault injection, timeouts) is tagged `sidecar` + `waypoint`. The waypoint suite
runs the full ambient path with ztunnel present, so the node+waypoint combination is exercised, not
just the waypoint in isolation.

There is deliberately **no combinatorial "dataplane flavor" axis**. Ambient is node + waypoint, and
both are covered by their own roles; the gateway roles are orthogonal to mesh traffic. If every
implementation conforms in every role it legitimately fills, the combinations follow; a separate
axis would multiply runtime without adding information.

Every feature's destination is a per-feature backend (its own echo deployment and Services) in the
workload namespace of the role's mode, so config and policies cannot leak between features running
in parallel. The host a feature configures serves **v1 only**: v2 is reachable only through config
under test. Without that, a routing test could pass by chance on a 50/50 load balancer once its
config is stripped, and the negative control would not be able to tell. For the waypoint role the
per-feature Service carries `istio.io/use-waypoint` and is attached to one shared waypoint per
proxy.

#### Todo: mode parity and interop

Role conformance says nothing about whether a feature behaves the *same* in sidecar and ambient
mode, nor about mixing the two. These are separate, smaller exercises, deliberately not part of the
support matrix:

- **Mode-diff suite.** Run the same feature set against reference proxies in sidecar mode and in
  ambient mode (with a waypoint for `RequiresL7` features) and compare the two observations against
  *each other* rather than against an expectation. This reports legitimate divergence without making
  it a conformance requirement (ambient without a waypoint is L4-only by design).
- **Interop suite.** Mixed pairs (sidecar → ambient, ambient → sidecar), policies owned by one side
  and enforced by the other, and waypoints in front of sidecar workloads. Same features, different
  topology.

### 3.10 Relationship to the existing integration tests

PCS does not replace `tests/integration/{pilot,security,telemetry,ambient}` and does not extend them
in place. The two suites answer different questions and carry different failure semantics:

- **Integration tests ask "did we break Istio?"** They must be green; a failure is a bug to fix.
- **PCS asks "does proxy Y implement feature X in role Z?"** For an incomplete implementation the
  suite is *expected* to be red, and that red is the data. Blocking is on regressions against the
  declared manifest (§3.7), not on absolute parity.

This is why folding PCS into the existing suites does not work. Their assertions are written against
Envoy internals (config dumps, admin endpoints, `istioctl proxy-config`, xDS goldens), so making them
proxy-neutral would remove the checks that catch Envoy regressions. They also share deployments and
test combinations, while the matrix needs one behavior in isolation with a per-feature backend to be
attributable to the proxy under test. And only a subset of roles applies to each implementation:
agentgateway has no sidecar or egress implementation, so most suites have nothing to attach it to.
The coverage check (§3.5) — surface from protobuf reflection, `Covers`, exemptions, the ratchet, the
release-note gate — has no equivalent in ad-hoc regression tests, and is the main reason the suite
must be its own thing.

**The rule: the assertion surface and the question decide, not the feature area.**

| | PCS | Integration |
|---|---|---|
| Question | Is feature X implemented by proxy Y in role Z? | Did we break Istio? Does topology T work? |
| Assertion | Black-box only (§3.2): echo-observed status, headers, protocol, timing; Kubernetes status; standard telemetry | Anything, including Envoy internals, config dumps, admin endpoints, xDS goldens |
| Axis | One feature × implementation in a role | Topology and combination: multi-cluster, multi-network, revisions, VM, external istiod, upgrades, Helm |
| Isolation | One behavior per feature, per-feature backend, shared gateway per proxy | Realistic shared deployments, cross-feature interaction |
| Validity | Must pass on the reference proxy *and* fail when its config is stripped (§3.4) | Ordinary test; no negative-control requirement |
| Defaults | Product defaults (PCS restores Istio's retry default) | Test-environment defaults (`tests/integration/base.yaml`) |
| Lifecycle | Stable feature ID, `Covers`, coverage check, declared support | Ordinary test, free to change |

So new proxy-neutral behavior tests go to PCS; new Envoy-specific behavior, topology combinations and
regression coverage go to integration.

**Overlap in coverage is fine; duplicate assertions are not.** A behavior may appear in both suites
when the integration variant adds something PCS deliberately excludes — Envoy internals, a topology
combination, an upgrade path. When it adds nothing, the PCS feature is the canonical test and the
integration test should be retired as `routing.go` shrinks (§9.4); leaving both in place means the
two drift and both need maintenance. Share helpers in `pkg/test` (echo, `check`), not test bodies: a
shared feature would couple the stable conformance contract to a freely changing regression test.
Upstream Gateway API semantics stay with `TestGatewayAPIConformance`, not PCS (§3.3). The two
implementation-specific suites (`pilot/agentgateway`, `ambient/agentgateway`) are the first
candidates to fold into the corresponding PCS roles and delete.

## 4. PoC results: ingress gateway role

Run on 2026-11-10 against a kind cluster, upstream images, both ingress gateway implementations.
The egress role was split out after this run and has only its first two features; it is not
measured here.

| | Envoy (`istio`) | agentgateway (`istio-agentgateway`) |
|---|---|---|
| Features passing | **140 / 142**¹ | **31 / 142** |

¹ 139 in the last full run; `backend.destinationrule.lb-round-robin` passed in a rerun after a test
fix. The 2 remaining failures are finding 2 below.

| Coverage check | |
|---|---|
| Surface entries covered by features | 311 / 1,374 |
| Exempted with a reason | 27 |
| Backlog (`todo`, ratchet) | 724 |

| Negative controls (Envoy) | |
|---|---|
| Control OK (test fails without its config) | 112 |
| Weak | 0 (6 found and fixed during the PoC) |
| Not applicable (nothing to strip, or `NoControl`) | 30 |

### 4.1 Findings

1. **agentgateway ignores Istio policy APIs.** AuthorizationPolicy and RequestAuthentication are
   accepted and not enforced (a DENY fails open). VirtualService, DestinationRule, Istio Gateway,
   Telemetry, ProxyConfig `gatewayTopology`/`proxyHeaders`/`connectionSettings` and listener
   `tls.options` have no effect either. Also missing: XFF append and trust, XFCC sanitizing,
   Istio-format metrics, path normalization, case-insensitive Host matching.
   It does support the Istio deployment controller features (except manual deployment), mesh mTLS to
   backends, ServiceEntry backends and the protocol basics.
2. **istiod bug: Telemetry access-log disable.** `accessLogging: [{disabled: true}]` without a named
   provider has no effect when logs come from `meshConfig.accessLogFile` and no default access-log
   providers are set. `mergeLogs` (`pilot/pkg/model/telemetry.go:618`) produces an empty list, which
   `setHTTPAccessLog` (`pilot/pkg/networking/core/accesslog.go:205`) treats as "no Telemetry" and
   falls back to the file log, contradicting the contract at `telemetry.go:240`. Naming the provider
   works.
3. **Usability: Istio Gateway TLS on Gateway-API-deployed pods fails silently.** istiod serves an
   Istio-Gateway `credentialName` (and DestinationRule client keys) only to proxies whose service
   account may read Secrets. Auto-deployed gateways get no such Role. The listener exists but resets
   connections, the secret stays warming, there is no status, and the only signal is an istiod
   warning.

### 4.2 Lessons for the suite

- Most early Envoy "failures" were wrong test assumptions about Envoy semantics (e.g. `APPEND_FORWARD`
  only forwards XFCC for mTLS clients; `X-Forwarded-Host` is only added on host rewrite;
  `httpRequestTimeout` bounds receiving the request, not the backend wait). Envoy as the reference
  plus hand-debugging of every unexplained failure was necessary before any result could be trusted.
- A second implementation is an excellent negative control: agentgateway exposed every weak test the
  systematic controls later confirmed.
- Echo limitations constrain what can be observed: its WebSocket client drops custom Host headers, and
  it only sends PROXY headers over raw TCP. Both are worth fixing in echo (§9.4).

## 5. Step 2: Make gaps visible

Most of the immediate user value doesn't need a generator rewrite. A capability and status layer:

1. computes **which features a proxy's effective config uses**;
2. intersects them with the **capability set declared by the target implementation**;
3. writes **status conditions** (e.g. `reason=UnsupportedByImplementation`) for anything not covered,
   and **fails closed** for security policy the proxy can't enforce.

This fixes finding 1's worst aspect, a DENY that fails open, without waiting for convergence. Istio
already has policy-accepted conditions for waypoints and ztunnel; this extends them per
implementation. `istioctl analyze` can consume the same capability sets to warn before apply. It is
also code a shared IR needs anyway, as its validation pass, so it is a low-commitment first PR.

Estimated effort: about 1–2 engineer-months.

## 6. Step 3: Converge on a shared intermediate representation

### 6.1 Why

Two (and with ztunnel, three) independent translation pipelines mean parity depends on codebases
evolving in lockstep. A proxy-neutral IR that every implementation lowers from:

- gives a single place to compute and report "unsupported by implementation X" (§5);
- allows cheap, unit-level conformance tests at the IR→proxy boundary, complementing the slower
  integration suite;
- makes a new proxy mostly a lowering backend (§7).

Istio already ships two proxy-neutral wire formats (the workload API and the agentgateway API), so the
question is whether Envoy gets one too, not whether to introduce the concept. Scope here is an IR
**inside istiod**; whether it also becomes Envoy's wire format is a later decision.

### 6.2 Effort

Rough order-of-magnitude estimates in engineer-months, based on code size. Not a detailed plan.

| Component | Eng-months | Notes |
|---|---|---|
| IR schema design and maintainer agreement | 1–2 | The gating item. Cheapest option: start from the agentgateway API, or a fork of it. |
| Gateway API → IR | ~0.5 | Mostly reuse the agentgateway translator. |
| Istio APIs → IR | 3–5 | VirtualService semantics are the hard part: delegation, merging across gateways, host matching, `Sidecar` scoping. Also DR subsets, AuthorizationPolicy, PeerAuthentication, RequestAuthentication, Telemetry. |
| IR → agentgateway | ~0.5 | Nearly a no-op if the IR is shaped like the agentgateway API. |
| IR → Envoy | 9–15 | A rewrite of route/cluster/listener/authz generation for sidecars, gateways and waypoints. Most of the cost is matching today's behavior exactly, not writing new code. |
| Push performance and benchmarking | 1–2 | xDS generation is the scaling hotspot; an extra layer adds memory use and allocations. |

**Total for a full IR: about 1.5–2.5 engineer-years.** `EnvoyFilter` stays as a post-lowering patch
on Envoy xDS, outside the IR by definition. The work cuts across about 40 files in
`pilot/pkg/networking/core` that change every release, so it has to land gradually behind a feature
flag.

### 6.3 Recommended first slice: waypoints (about 6–9 engineer-months)

- Waypoints are a role where two implementations already compete.
- The Envoy waypoint path (`listener_waypoint.go`, `cluster_waypoint.go`, `waypoint.go`) is much
  smaller than the sidecar path, has no `Sidecar` scoping, and attaches policy mostly via `targetRefs`.
- Both implementations would consume the same IR, which proves the concept.
- Sidecars and gateways stay on the existing path until the waypoint IR has shown parity.
- **Safety nets:** PCS proves implementations agree with each other; an **output-diff harness**
  (built on the simulation and golden tests in `pilot/pkg/networking/core`) proves the refactor didn't
  change Envoy's xDS, including what black-box tests can't see (route ordering, stat names, the xDS
  `EnvoyFilter` patches depend on). The refactor needs both.

### 6.4 Can the agentgateway API carry Istio's semantics?

Checked against `github.com/agentgateway/agentgateway/api@v0.0.0-20261027162624-26f5aebe99a3`
(`resource.pb.go`), the version pinned in `go.mod`. This is a paper analysis; none of these mappings
has been implemented or tested yet.

| Role | Fits the agentgateway API? |
|---|---|
| Ingress / egress gateway | Yes, with resilience fields (gap 3) and AUDIT (gap 2) |
| Waypoint | Yes, with attachment extensions (gap 1), flattening in istiod (gap 2), and resilience fields (gap 3) |
| Node (ztunnel) | Already uses the workload API (WDS); no change needed |
| Sidecar | No. Needs a new capture model (gap 4) |

**Already maps well**

| Istio feature | agentgateway API |
|---|---|
| Route matches, weights, header modification, redirect, rewrite, direct response, CORS | `Route`, `RouteMatch`, `TrafficPolicySpec_*`, close to 1:1 |
| Mirroring with percentage | `RequestMirrors_Mirror.Percentage` |
| Timeouts | `Timeout{Request, BackendRequest, ResponseIdle}` |
| Retries | `Retry{RetryStatusCodes, Attempts, Backoff, Condition (CEL)}` |
| Fault delay | `Delay` (CEL) |
| `AuthorizationPolicy` ALLOW/DENY, L4 and L7 | `TrafficPolicySpec_RBAC` / `FrontendPolicySpec_NetworkAuthorization`: allow/deny/require as CEL. Principals, namespaces, IP blocks, paths, methods and claims are all expressible. |
| `AuthorizationPolicy` CUSTOM | `ExtAuthz` |
| `RequestAuthentication` | `TrafficPolicySpec_JWT` |
| TLS origination | `BackendPolicySpec_BackendTLS` (SAN verification, SNI, certificate source) |
| Consistent hash | `SessionAffinity.Source` (CEL) |
| Locality / failover | workload API `LoadBalancing` (routing preference + STRICT/FAILOVER), shared with ztunnel |
| Tracing, access logs, metrics | `FrontendPolicySpec_Tracing` (OTLP, CEL sampling), `Logging`, `Metrics` |
| TCP/TLS routes, ServiceEntry, services and workloads | `TCPRoute`, `workloadapi.Service`/`Workload` |

The API already has policy attachment with inheritance (`Policy{Target, Inheritance,
CreationTimestamp}`, targets `Gateway | Route | Backend | Service | ListenerSet`).

**Gap 1: attachment and scoping (small, additive).** `PolicyTarget` cannot express a workload
selector, namespace-wide or mesh-wide scope, direction (inbound vs. outbound, needed for sidecars), or
port-level settings. Proposed: `PolicyTarget_Workload`, `Namespace`/`Mesh` targets, and a
direction/port qualifier. This would also benefit agentgateway in waypoint mode.

**Gap 2: precedence (resolved in istiod; no wire change except AUDIT).** agentgateway uses Gateway
API-style precedence; Istio authz is additive (CUSTOM → DENY → ALLOW across mesh, namespace and
workload). istiod flattens applicable policies into one RBAC block per target, and likewise resolves
VirtualService delegation, DestinationRule merging and `exportTo`. AUDIT has no counterpart.

**Gap 3: resilience fields (new fields).** No counterpart for connection-pool limits
(`maxRequests`, `maxPendingRequests`, `maxRequestsPerConnection`, retry budgets); outlier detection is
an EWMA health score rather than Envoy's consecutive-error counters (PCS would report `Partial`); no
LB algorithm choice or weighted locality `distribute`; no subset backend reference; per-try timeout,
connection-level `retryOn` and fault abort percentage are unconfirmed.

**Gap 4: sidecar data plane model (large; needs design on the agentgateway side).** `Bind`/`Listener`
assume explicit listeners. There is no transparent capture, original-destination handling,
per-service-port outbound listeners, inbound passthrough, protocol sniffing, PERMISSIVE mTLS, or
`Sidecar` egress scoping. This needs a first-class "capture" bind, and agentgateway has no sidecar use
case driving it today.

**Gap 5: Envoy-only extensions (out of scope).** `EnvoyFilter`, `WasmPlugin` and Lua
`TrafficExtension` stay outside the IR. A typed per-implementation escape hatch could let other
implementations reject them visibly instead of ignoring them.

**Keep the IR structured, not CEL.** Today istiod validates CEL and passes it through only where Envoy
evaluates CEL natively (access-log filters and formatters). Many agentgateway API fields are CEL
strings, and Envoy has no CEL hook for most of them:

| agentgateway field | Envoy evaluates CEL there? |
|---|---|
| RBAC allow/deny | **Yes**: RBAC `Policy.condition` |
| `Retry.Condition` | No. Envoy uses `retry_on` + status codes |
| `Delay.Duration` | No. Envoy uses a fixed delay + percentage |
| `SessionAffinity.Source` | No. Envoy uses header/cookie/source-IP hash policies |
| `Health.UnhealthyCondition` | No. Envoy outlier detection is counter-based |
| Tracing sampling | No. Envoy uses fixed percentages |

So the IR inside istiod should be structured and Istio-owned, but **deliberately shaped like the
agentgateway API** (same resource kinds, keys and attachment model) plus the extensions above.
Lowering to agentgateway prints CEL where its API needs it; lowering to Envoy works from structure, so
nothing is lost. User-authored CEL runs only where it can be evaluated (on Envoy: RBAC) and is
visibly rejected elsewhere. Once the shapes converge, the shared subset can be proposed upstream to
agentgateway, or as a neutral `istio.io` API.

## 7. Adopting a new proxy

With all three steps in place, adding a proxy becomes a bounded, checkable piece of work:

1. **Deploy:** provide a GatewayClass and deployment template (or the role-specific equivalent).
2. **Run the suite:** add the proxy to the proxy list and get a matrix on day one, including upstream
   Gateway API conformance with all features enabled.
3. **Declare capabilities:** publish what the proxy supports. istiod reports everything else to users
   instead of ignoring it (§5), and CI holds the declaration to the measured matrix (§3.7).
4. **Lower from the IR:** implement a lowering backend instead of a translation pipeline (§6).

"Done" is then defined by data: the declared capabilities match the matrix, and the matrix covers the
tracked API surface.

## 8. Rollout

| Phase | Scope | Status |
|---|---|---|
| 0 | Design agreed | This document |
| 1 | PCS for ingress gateways: harness, features, upstream Gateway API conformance per proxy | **PoC done:** 142 features, Envoy and agentgateway (§4) |
| 2 | Coverage check on every PR | **PoC done:** surface, exemptions, ratchet, flag and key tracking (§3.5) |
| 2b | Negative controls | **PoC done:** `TestIngressConformanceControls`, `TestEgressConformanceControls`, `TestSidecarConformanceControls` (§3.4) |
| 3 | PCS for sidecar, ztunnel, waypoints, egress (§3.9); port the bulk of `routing.go` and the ambient traffic tests; publish the matrix on istio.io | **In progress:** sidecar, node, waypoint and egress suites with the first `mesh.*` and `egress.*` features; mode-diff and interop suites pending |
| 4 | Capability and status layer (§5); outcomes and declared manifests (§3.7) | Next, can run in parallel with 3 |
| 5 | Output-diff harness (§6.3) | Before 6 |
| 6 | IR, waypoints first (§6.3), then gateways and sidecars | Gated on 3–5 and maintainer agreement |

Ingress gateways came first because both implementations already run Gateway API conformance
there, and agentgateway's ingress support is more mature than its waypoint support, so the first
matrix measures behavior differences rather than mostly missing features. ztunnel stays in scope: no
replacement is expected soon, but it benefits from the same regression coverage, and dropping it
later is cheap.

## 9. Other improvements

### 9.1 Separate role from implementation in the proxy model
`pkg/model/proxy.go` defines `NodeType` as `sidecar | router | waypoint | ztunnel | agentgateway`,
mixing role with implementation. An agentgateway waypoint and an agentgateway ingress share one
`NodeType`, and `node.IsAgentgateway()` checks spread through push and xDS code. Proposal:
`Proxy.Role` plus `Proxy.Implementation` (or a capabilities struct), with generation gated on
capabilities instead of type checks.

### 9.2 Standardize the observable contract
- **Metrics:** pin the standard Istio metric names, labels and semantics as a spec every proxy must
  meet; PCS telemetry features test against it.
- **Waypoint traversal:** tests prove traversal by adding a header through a route filter, which is
  itself a feature under test. A standard traversal signal (or `istio_requests_total{reporter="waypoint"}`)
  would let traversal be checked independently.

### 9.3 Fix silent failures found by the PoC
- Telemetry access-log disable (§4.1, finding 2).
- Istio Gateway `credentialName` on Gateway-API-deployed pods (§4.1, finding 3): either grant the
  auto-deployed service account the secret Role, or report the problem in status.

### 9.4 Test framework
- Echo: send the requested Host header on WebSocket upgrades; support PROXY protocol over HTTP.
- Generalize `Settings().Agentgateway` into a list of enabled implementations and roles.
- Move the two duplicated Gateway API conformance runners (`pilot/gateway_conformance_test.go`,
  `pilot/agentgateway/gateway_conformance_test.go`) onto a shared helper parameterized by
  GatewayClass.
- New behavior tests should go to PCS; over time `routing.go` shrinks to Envoy-specific behavior
  (§3.10).
- `istioctl` debugging is Envoy-centric; `istioctl analyze` could consume the capability sets (§5).

## 10. Open questions

1. IR (§6): adopt the agentgateway API, fork it into an Istio-owned API, or design a new one? This
   determines governance: who can evolve the schema, and on which release cadence.
2. Should `Partial` be allowed for security features, or must they be all-or-nothing?
3. Is the istio.io matrix keyed by API field (granular) or by feature (readable)? Proposal: feature
   IDs with optional `/subcase` suffix.
4. Where does the declared manifest for an out-of-tree proxy live: in-tree, so the project vouches for
   the claim, or in the proxy's repo with only the report imported?
5. Multicluster and multi-network: a separate role dimension, or a topology variant of each proxy?
6. CI cost: the full gateway suite takes tens of minutes per proxy. Run it per PR for affected paths,
   periodically, or both?
