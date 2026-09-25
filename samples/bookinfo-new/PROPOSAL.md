# Note: a pure-Go Bookinfo (samples/bookinfo-new)

Hi — I've re-implemented the Bookinfo sample in pure Go (stdlib only, no
external dependencies) under `samples/bookinfo-new`, with identical service
names, ports, endpoints, env vars, and JSON shapes, so every Istio networking
demo (routing, fault injection, mirroring, rate limiting, egress, ...) works
unchanged. Here's why I think it's worth keeping alongside the original:

## Faster, cheaper, more reliable

- **One toolchain, not five.** The original is Ruby + Python + Node + Java
  (Liberty) + MongoDB + MySQL. Fixing a sample bug today means knowing all of
  them; here it's Go — the language Istio itself is written in.
- **CI builds 8 small images from 4 binaries in seconds** (no
  pip/npm/gem/gradle resolution; the original needs ~10 images plus
  mongodb/mysql). The Liberty image alone is the
  slowest, most flaky build in the sample today.
- **Images are ~8 MB each** (`FROM scratch`: one static binary + CA bundle)
  vs 200 MB–1 GB for the current ones. Tutorial users pull them on the first
  run of the docs; that's minutes of waiting and bandwidth saved per reader.
- **No databases.** The mongodb/mysql deployments and seed scripts disappear;
  ratings v2 serves the same seeded values from memory. Fewer moving parts
  means fewer "the sample doesn't work" issues that aren't actually Istio
  bugs.

## Smaller attack surface

No Python/Node/Ruby/Java dependency trees to audit: zero transitive
dependencies in the Go code, and scratch images carry no runtime CVEs. For
sample code that everyone runs on day one, that's a meaningful difference.

## What we give up (and why it's acceptable)

- **The polyglot story.** Bookinfo was deliberately multi-language to show
  Istio is language-agnostic. I'd keep the original for that demo, and
  suggest `bookinfo-new` become the default for the traffic-management tasks
  and tutorials, where the languages are incidental and just slow things down.
  Both can coexist; the networking YAMLs are shared.
- **A few fidelity details** are simplified for demo purposes (unsigned
  session cookie, core B3/W3C trace headers instead of the full vendor list,
  no legacy gRPC port on reviews). All of it is documented in the README.

Everything is verified end-to-end locally (`run-local.sh`), and because the
services are drop-in compatible, the original manifests in
`samples/bookinfo/platform/kube/` are reused unchanged (image names
substituted). Happy to iterate on the naming or scope — the core ask is just:
give the docs a fast, boring, dependency-free Bookinfo to run the demos on.
