#!/usr/bin/env bash
# Runs the shared integration suite against a CINC Server Erlang stack in AWS.
#
#   ./run-cinc-server-erlang.sh            apply, test, destroy (always)
#   KEEP=1 ./run-cinc-server-erlang.sh     apply and test; leave the stack up
#   ./run-cinc-server-erlang.sh destroy    tear down a kept stack
#
# Needs terraform and go, and AWS credentials in the environment or profile.
# Extra arguments to `go test` can be passed in GOTESTFLAGS, e.g.
#   GOTESTFLAGS='-run TestCincServerErlang/cookbooks/' KEEP=1 ./run-cinc-server-erlang.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tf_dir="$here/terraform"

for tool in terraform go; do
  command -v "$tool" >/dev/null || { echo "error: $tool is not installed" >&2; exit 1; }
done

tf() { terraform -chdir="$tf_dir" "$@"; }

if [[ "${1:-}" == "destroy" ]]; then
  tf destroy -auto-approve -input=false
  exit
fi

tf init -input=false >/dev/null

if [[ "${KEEP:-}" != "1" ]]; then
  # Destroy on every exit, pass or fail, so a run never leaves billable
  # resources behind.
  trap 'echo "Destroying the stack..."; tf destroy -auto-approve -input=false >/dev/null' EXIT
fi

tf apply -auto-approve -input=false
echo "Server: $(tf output -raw server_url) — waiting for the bootstrap (10–15 minutes on a new stack)..."

cd "$here"
# shellcheck disable=SC2086 # GOTESTFLAGS is split on purpose.
CINC_SERVER_ERLANG_TARGET="$(tf output -raw target_file)" \
  go test -count=1 -timeout 60m -v ${GOTESTFLAGS:-} ./cincservererlang/
