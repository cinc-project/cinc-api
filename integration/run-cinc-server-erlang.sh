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

# check_leftovers lists anything still carrying the stack's tag after a
# destroy. Terraform removes only what its state tracks, and the AWS provider
# can orphan a resource when it silently retries a create (seen with the VPC
# and Elastic IP on the first run), so look rather than assume. Needs the AWS
# CLI; skipped without it.
check_leftovers() {
  command -v aws >/dev/null || return 0
  local region="${TF_VAR_region:-us-west-2}" filter="Name=tag:cinc-api-integration,Values=true" found
  found="$(
    aws ec2 describe-addresses --region "$region" --filters "$filter" --query 'Addresses[].AllocationId' --output text
    aws ec2 describe-vpcs --region "$region" --filters "$filter" --query 'Vpcs[].VpcId' --output text
    aws ec2 describe-instances --region "$region" --filters "$filter" Name=instance-state-name,Values=pending,running,stopping,stopped --query 'Reservations[].Instances[].InstanceId' --output text
  )"
  if [[ -n "${found//[[:space:]]/}" ]]; then
    echo "warning: resources tagged cinc-api-integration=true remain in $region after destroy:" >&2
    echo "$found" >&2
    echo "They are not in Terraform's state; delete them by hand (see integration/README.md)." >&2
  fi
}

if [[ "${1:-}" == "destroy" ]]; then
  tf destroy -auto-approve -input=false
  check_leftovers
  exit
fi

tf init -input=false >/dev/null

if [[ "${KEEP:-}" != "1" ]]; then
  # Destroy on every exit, pass or fail, so a run never leaves billable
  # resources behind.
  trap 'echo "Destroying the stack..."; tf destroy -auto-approve -input=false >/dev/null; check_leftovers' EXIT
fi

tf apply -auto-approve -input=false
echo "Server: $(tf output -raw server_url) — waiting for the bootstrap (about 5 minutes on a new stack)..."

cd "$here"
# shellcheck disable=SC2086 # GOTESTFLAGS is split on purpose.
CINC_SERVER_ERLANG_TARGET="$(tf output -raw target_file)" \
  go test -count=1 -timeout 60m -v ${GOTESTFLAGS:-} ./cincservererlang/
