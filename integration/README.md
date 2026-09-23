# Integration tests

End-to-end tests of the public client against real server implementations.
This directory is its own Go module, so its test dependencies never reach
consumers of `github.com/cinc-project/cinc-api`.

| Package | What it is | When it runs |
|---|---|---|
| `suite/` | Every test, written once against a `suite.Target` | — |
| `cincserverng/` | Runs the suite against an in-memory [cinc-server-ng](https://github.com/cinc-project/cinc-server-ng) | Every CI job; `go test ./...` here (~2 s) |
| `cincservererlang/` | Runs the suite against the CINC Server Erlang stack (erchef) in AWS | By hand, with `run-cinc-server-erlang.sh` |
| `terraform/` | The AWS stack for `cincservererlang` | By hand, through the script |

A case that one server cannot pass is listed in that target's `Gaps` with the
reason: an upstream cinc-server-ng issue, or the behaviour observed on
cinc-server-erlang. On cinc-server-erlang the only gaps are operations erchef
reserves to the pivotal superuser (creating organizations,
`/authenticate_user`, associating a user without an invitation); the tests'
admin is a server-admin, and pivotal's key never leaves the instance. `suite.Run` rejects a gap that names no test or has no
reason.

## Running against cinc-server-erlang

Needs `terraform`, `go`, and AWS credentials in the environment or an AWS
profile (`AWS_PROFILE=...`). Nothing secret is committed; the admin key, the
test CA and the stats password are generated on your machine and kept in
local, gitignored Terraform state and `terraform/.out/`.

```sh
./run-cinc-server-erlang.sh            # apply, test, destroy (always)
KEEP=1 ./run-cinc-server-erlang.sh     # apply and test; leave the stack up
./run-cinc-server-erlang.sh destroy    # tear down a kept stack
```

With a kept stack, re-run the tests directly:

```sh
cd cincservererlang && go test -count=1 -v ./...
GOTESTFLAGS='-run TestCincServerErlang/cookbooks/' KEEP=1 ../run-cinc-server-erlang.sh
```

`go test` reads `terraform/.out/target.json`, or the file named by
`CINC_SERVER_ERLANG_TARGET`. Without it the package skips.

### What the stack creates

In `us-west-2` by default (`-var region=...`):

- A VPC with one public subnet, an internet gateway and a route table.
- A security group allowing HTTPS (443) from your current public IP only
  (`-var allowed_cidr=...` to change). No SSH.
- One `t3.xlarge` Ubuntu 22.04 instance with an Elastic IP, an encrypted 50 GB
  root volume, IMDSv2, and an instance profile for SSM Session Manager.
- CINC Server `15.10.125` from the stable channel, installed from the package
  omnitruck lists, checked against its SHA-256. The bootstrap creates the org
  `cincapi` and the admin `cincapi-admin` (an org member and server-admin)
  authenticating with the Terraform-generated key.

TLS is verified: Terraform generates a throwaway CA and a certificate for the
Elastic IP, and the tests trust only that CA. The certificate's key reaches the
instance through user-data, so anyone in the account who can read the
instance's user-data can see it (and the stats password); both protect only
this short-lived server.

The instance shuts itself down (and, with `terminate` shutdown behaviour, is
terminated) four hours after boot, so a forgotten stack stops billing for
compute. The Elastic IP and VPC remain until `destroy`.

Everything the stack creates is tagged `cinc-api-integration=true`. After
every destroy the script lists anything still carrying that tag (when the AWS
CLI is installed): Terraform removes only what its state tracks, and the AWS
provider can leave an untracked duplicate behind when it silently retries a
create. Delete such leftovers by hand, e.g.
`aws ec2 release-address --allocation-id ...` and `aws ec2 delete-vpc --vpc-id ...`.

### Cost and time

About $0.17/hour for the `t3.xlarge` plus the Elastic IP. A full run takes
about 10 minutes: roughly 1 for `apply`, 5 for the package install and two
`reconfigure`s, 3 for the suite, and 1 for `destroy`.

### Debugging

```sh
aws ssm start-session --target "$(terraform -chdir=terraform output -raw instance_id)"
sudo tail -f /var/log/cloud-init-output.log   # the bootstrap
sudo cinc-server-ctl status
```

`/etc/cinc-api-it/ready` exists once the bootstrap has finished.
