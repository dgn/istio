#!/bin/bash
#
# Copyright Istio Authors
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

# Run all four pure-Go Bookinfo services on localhost so the sample can be
# tried without a cluster. Requires only a Go toolchain (no Docker needed).
#
#   ./run-local.sh            # start everything
#   ./run-local.sh stop       # stop everything
#
# Then open http://localhost:9080/

set -o errexit

SCRIPTDIR=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )
BUILD_DIR="${SCRIPTDIR}/bin"
PIDFILE="${BUILD_DIR}/pids"

# Service -> local port
PORT_DETAILS=9082
PORT_RATINGS=9083
PORT_REVIEWS=9084
PORT_PRODUCTPAGE=9080

start() {
  # Replace any running instance so we never serve a stale binary.
  stop
  sleep 0.5

  mkdir -p "${BUILD_DIR}/logs"
  ( cd "${SCRIPTDIR}" && go build -o "${BUILD_DIR}" ./cmd/... )
  echo "built into ${BUILD_DIR}"

  # Log to files so the script (and any pipe it is attached to) can exit.
  "${BUILD_DIR}/details" "${PORT_DETAILS}" > "${BUILD_DIR}/logs/details.log" 2>&1 &
  echo $! > "${PIDFILE}.details"
  SERVICE_VERSION="${SERVICE_VERSION:-v1}" "${BUILD_DIR}/ratings" "${PORT_RATINGS}" > "${BUILD_DIR}/logs/ratings.log" 2>&1 &
  echo $! > "${PIDFILE}.ratings"
  ENABLE_RATINGS="${ENABLE_RATINGS:-true}" STAR_COLOR="${STAR_COLOR:-black}" \
    RATINGS_HOSTNAME=localhost RATINGS_SERVICE_PORT="${PORT_RATINGS}" \
    "${BUILD_DIR}/reviews" "${PORT_REVIEWS}" > "${BUILD_DIR}/logs/reviews.log" 2>&1 &
  echo $! > "${PIDFILE}.reviews"
  DETAILS_HOSTNAME=localhost DETAILS_SERVICE_PORT="${PORT_DETAILS}" \
    REVIEWS_HOSTNAME=localhost REVIEWS_SERVICE_PORT="${PORT_REVIEWS}" \
    RATINGS_HOSTNAME=localhost RATINGS_SERVICE_PORT="${PORT_RATINGS}" \
    "${BUILD_DIR}/productpage" "${PORT_PRODUCTPAGE}" > "${BUILD_DIR}/logs/productpage.log" 2>&1 &
  echo $! > "${PIDFILE}.productpage"

  sleep 1
  echo
  echo "Bookinfo (pure Go) running (logs in ${BUILD_DIR}/logs):"
  echo "  productpage  http://localhost:${PORT_PRODUCTPAGE}/"
  echo "  details      http://localhost:${PORT_DETAILS}/details/0"
  echo "  reviews      http://localhost:${PORT_REVIEWS}/reviews/0"
  echo "  ratings      http://localhost:${PORT_RATINGS}/ratings/0"
  echo
  echo "stop with: $0 stop"
}

stop() {
  local f
  for f in "${PIDFILE}".*; do
    [[ -f "${f}" ]] || continue
    kill "$(cat "${f}")" 2>/dev/null || true
    rm -f "${f}"
  done
}

case "${1:-start}" in
  stop) stop; echo "stopped" ;;
  start) start ;;
  *) echo "usage: $0 [start|stop]" >&2; exit 1 ;;
esac
