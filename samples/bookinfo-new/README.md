# Bookinfo (pure Go)

A re-implementation of the [Bookinfo sample](../bookinfo) in **pure Go**, using
only the standard library — no external Go dependencies, no databases, no
language runtimes. All data is hard-coded or kept in memory.

The four services expose the same endpoints, ports, environment variables, and
behavior as the original sample, so all of the Istio traffic management demos
(routing, fault injection, mirroring, rate limiting, circuit breaking,
egress policies, ...) work unchanged with the manifests in
`samples/bookinfo/networking`.

| Service     | Original            | Go implementation                          | Port |
|-------------|---------------------|--------------------------------------------|------|
| `productpage` | Python / Flask     | `cmd/productpage` (templates + static assets embedded via `go:embed`) | 9080 |
| `details`   | Ruby / WEBrick     | `cmd/details`                              | 9080 |
| `reviews`   | Java / WebSphere Liberty | `cmd/reviews`                        | 9080 |
| `ratings`   | Node.js + MongoDB/MySQL | `cmd/ratings` (seeded data hard-coded) | 9080 |

The `mongodb` and `mysql` deployments are **not** needed: `ratings` v2 serves
the same seeded values (`Reviewer1: 5`, `Reviewer2: 4`) from memory.

## Differences from the original

- **No databases.** `ratings` v2 reads its "database" from hard-coded values
  matching the original seed scripts.
- **Sessions.** `productpage` uses a plain (unsigned) cookie instead of
  Flask's `itsdangerous`-signed session — fine for a demo.
- **Tracing.** This version forwards the core tracing headers (x-request-id
  plus the B3 and W3C Trace Context schemes that Istio uses; see
  `internal/bookinfo/headers.go`) rather than the original's full list of
  vendor-specific headers. Sidecar-generated spans still work since the
  headers are propagated.
- **Metrics counting.** The `request_result` counter records every upstream
  attempt (the original only counted the final outcome of the reviews
  retry), so a failing reviews request increments it twice.
- **`/metrics`.** Emits the same `request_result` counter in Prometheus text
  format (hand-rolled, no `prometheus/client_golang`).
- **gRPC.** The original `reviews` also served gRPC on 9081 for old
  productpage versions; the current sample only uses HTTP, so no gRPC here.
- Images are built `FROM scratch` (static binary + CA certificates).

## Behavior selected by environment variables

Same knobs as the original images:

| Variable                      | Service     | Effect                                                        |
|-------------------------------|-------------|---------------------------------------------------------------|
| `SERVICE_VERSION`             | ratings     | `v1` (default), `v2`, `v-faulty`, `v-delayed`, `v-unavailable`, `v-unhealthy` |
| `SERVICE_VERSION`             | details     | informational                                                 |
| `ENABLE_EXTERNAL_BOOK_SERVICE`| details     | `true`: fetch from the Google Books API (egress demo)         |
| `DO_NOT_ENCRYPT`              | details     | `true`: call the external API over HTTP (port 80)             |
| `ENABLE_RATINGS`              | reviews     | `true`: decorate reviews with star ratings (v2/v3)            |
| `STAR_COLOR`                  | reviews     | `black` (v2, 10s ratings timeout) or `red` (v3, 2.5s timeout) |
| `FLOOD_FACTOR`                | productpage | `>0`: fire N extra review requests per page view (rate limit demo) |
| `SERVICES_DOMAIN`             | all         | optional DNS suffix for cross-cluster setups                  |
| `DETAILS_HOSTNAME` / `DETAILS_SERVICE_PORT`, `REVIEWS_HOSTNAME` / `REVIEWS_SERVICE_PORT`, `RATINGS_HOSTNAME` / `RATINGS_SERVICE_PORT` | productpage, reviews | override upstream addresses |

## Endpoints

- `productpage`: `GET /` (alias `GET /index.html`), `GET /productpage`,
  `GET /health`, `POST /login`,
  `GET /logout`, `GET /metrics`, `GET /api/v1/products`,
  `GET /api/v1/products/{id}`, `GET /api/v1/products/{id}/reviews`,
  `GET /api/v1/products/{id}/ratings`, `GET /static/...`
- `details`: `GET /health`, `GET /details/{id}`
- `reviews`: `GET /health`, `GET /reviews/{id}`
- `ratings`: `GET /health`, `GET /ratings/{id}`, `POST /ratings/{id}`

## Try it locally (no Docker, no cluster)

```sh
cd samples/bookinfo-new
./run-local.sh
# open http://localhost:9080/
./run-local.sh stop
```

## Build the container images (`FROM scratch`)

```sh
cd samples/bookinfo-new
./build.sh                                # docker, all images, tag latest
CONTAINER_CLI=podman ./build.sh           # podman instead
HUB=ghcr.io/me TAGS=1.0 ./build.sh        # custom registry/tag
PUSH=1 HUB=ghcr.io/me ./build.sh          # build and push
```

This produces one image per version, mirroring the original sample's image
layout:

- `examples-bookinfo-go-productpage-v1`
- `examples-bookinfo-go-details-v1`, `examples-bookinfo-go-details-v2`
- `examples-bookinfo-go-reviews-v1`, `examples-bookinfo-go-reviews-v2`,
  `examples-bookinfo-go-reviews-v3`
- `examples-bookinfo-go-ratings-v1`, `examples-bookinfo-go-ratings-v2`

Each image is a single static binary plus the CA certificate bundle. The
per-version behavior (star color, external book API, "database-backed"
ratings) is baked in as `ENV` defaults — all eight images share the same
four binaries. Deployments can still override them at deploy time, e.g.
`kubectl set env deployment/ratings SERVICE_VERSION=v-faulty` for the fault
injection demo.

## Deploy to a cluster

The services are drop-in replacements (same names, ports, endpoints and
environment variables), so the original manifests in
`samples/bookinfo/platform/kube/` work as-is — only the image names need
swapping. From `samples/bookinfo/`:

1. Build and push the images (above).
2. Apply the sample, substituting the image names:

   ```sh
   cd platform/kube
   sub() { sed -E 's#registry.istio.io/release/examples-bookinfo-([a-z]+-v[0-9]+):[0-9.]+#myregistry.io/examples-bookinfo-go-\1:latest#g'; }
   sub < bookinfo.yaml | kubectl apply -f -
   kubectl apply -f ../networking/bookinfo-gateway.yaml
   kubectl apply -f ../networking/virtual-service-all-v1.yaml
   ```

3. Optional variants (same directory, apply with `sub < <file> | kubectl apply -f -`):

   - `bookinfo-details-v2.yaml` — details calling the Google Books API
   - `bookinfo-ratings-v2.yaml` — "database-backed" ratings. Runs with no
     real database: the data is in-memory.
   - flooding: `kubectl set env deployment/productpage FLOOD_FACTOR=100`
     (as in the original tutorial)

   `reviews-v2` (black stars) and `reviews-v3` (red stars) are already
   deployed by `bookinfo.yaml`; route to them with the virtual services in
   `samples/bookinfo/networking` (e.g. `virtual-service-reviews-v3.yaml`).

4. Clean up with `platform/kube/cleanup.sh`.
