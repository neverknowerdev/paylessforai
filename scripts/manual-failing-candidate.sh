#!/bin/sh
set -eu

mode="${PAYLESSFORAI_TEST_CANDIDATE_MODE:-}"
case "$mode:$1" in
  preflight-fail:--internal-preflight)
    echo "simulated migration/preflight failure" >&2
    exit 42
    ;;
  startup-fail:--internal-serve)
    echo "simulated unexpected startup failure" >&2
    exit 43
    ;;
esac

exit 0
