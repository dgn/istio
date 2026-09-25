#!/bin/bash
#
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

# Build (and optionally push) the pure-Go Bookinfo images (FROM scratch).
#
# Works with both docker and podman, selected via $CONTAINER_CLI (default:
# docker). All extra arguments are passed through to the build command
# (e.g. --platform linux/amd64,linux/arm64).
#
# Usage:
#   ./build.sh                          # build all images tagged latest
#   CONTAINER_CLI=podman ./build.sh
#   HUB=registry.example.com TAGS=1.0 ./build.sh
#   PUSH=1 HUB=registry.example.com ./build.sh

set -ox errexit pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

CLI="${CONTAINER_CLI:-docker}"
HUB="${HUB:-localhost:5000}"
TAGS="${TAGS:-latest}"

# <dockerfile target> <image name>
images="
productpage-v1 examples-bookinfo-go-productpage-v1
details-v1     examples-bookinfo-go-details-v1
details-v2     examples-bookinfo-go-details-v2
reviews-v1     examples-bookinfo-go-reviews-v1
reviews-v2     examples-bookinfo-go-reviews-v2
reviews-v3     examples-bookinfo-go-reviews-v3
ratings-v1     examples-bookinfo-go-ratings-v1
ratings-v2     examples-bookinfo-go-ratings-v2
"

while read -r target name; do
  [ -n "$target" ] || continue
  for tag in ${TAGS//,/ }; do
    echo ">> $CLI build $HUB/$name:$tag"
    $CLI build --target "$target" -t "$HUB/$name:$tag" "$@" .
    if [ "${PUSH:-0}" = "1" ]; then
      $CLI push "$HUB/$name:$tag"
    fi
  done
done <<< "$images"
