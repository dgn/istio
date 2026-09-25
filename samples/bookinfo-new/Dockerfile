# Copyright Istio Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Pure-Go Bookinfo services. There is one image per version, mirroring the
# original sample's image layout (details-v1/v2, reviews-v1/v2/v3,
# ratings-v1/v2, productpage-v1). Each final image is FROM scratch: a single
# static binary plus the CA certificate bundle (needed by the details service
# when it calls the external Google Books API). Per-version behavior is baked
# in as ENV defaults; deployments can still override them (e.g.
# SERVICE_VERSION=v-faulty for the fault injection demo).
#
# Build everything from this directory:
#
#   ./build.sh
#
# or build a single image:
#
#   docker build -t bookinfo/reviews-v2:latest --target reviews-v2 .

FROM golang:1.27-bookworm AS builder

WORKDIR /src

COPY go.mod ./
COPY internal/ internal/
COPY cmd/ cmd/

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN go build -trimpath -ldflags="-s -w" \
    -o /out/details ./cmd/details && \
    go build -trimpath -ldflags="-s -w" \
    -o /out/ratings ./cmd/ratings && \
    go build -trimpath -ldflags="-s -w" \
    -o /out/reviews ./cmd/reviews && \
    go build -trimpath -ldflags="-s -w" \
    -o /out/productpage ./cmd/productpage

# CA certificates for TLS to external services.
FROM scratch AS base
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

FROM base AS productpage-v1
COPY --from=builder /out/productpage /productpage
USER 1000
EXPOSE 9080
ENTRYPOINT ["/productpage"]
CMD ["9080"]

FROM base AS details-v1
COPY --from=builder /out/details /details
USER 1000
EXPOSE 9080
ENTRYPOINT ["/details"]
CMD ["9080"]

FROM base AS details-v2
ENV ENABLE_EXTERNAL_BOOK_SERVICE=true
COPY --from=builder /out/details /details
USER 1000
EXPOSE 9080
ENTRYPOINT ["/details"]
CMD ["9080"]

FROM base AS reviews-v1
COPY --from=builder /out/reviews /reviews
USER 1000
EXPOSE 9080
ENTRYPOINT ["/reviews"]
CMD ["9080"]

FROM base AS reviews-v2
ENV ENABLE_RATINGS=true STAR_COLOR=black SERVICE_VERSION=v2
COPY --from=builder /out/reviews /reviews
USER 1000
EXPOSE 9080
ENTRYPOINT ["/reviews"]
CMD ["9080"]

FROM base AS reviews-v3
ENV ENABLE_RATINGS=true STAR_COLOR=red SERVICE_VERSION=v3
COPY --from=builder /out/reviews /reviews
USER 1000
EXPOSE 9080
ENTRYPOINT ["/reviews"]
CMD ["9080"]

FROM base AS ratings-v1
COPY --from=builder /out/ratings /ratings
USER 1000
EXPOSE 9080
ENTRYPOINT ["/ratings"]
CMD ["9080"]

FROM base AS ratings-v2
ENV SERVICE_VERSION=v2
COPY --from=builder /out/ratings /ratings
USER 1000
EXPOSE 9080
ENTRYPOINT ["/ratings"]
CMD ["9080"]
