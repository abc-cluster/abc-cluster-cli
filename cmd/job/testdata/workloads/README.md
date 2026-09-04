# Workload script fixtures

Inputs for the `TestJobRun_Workload*` tests in `cmd/job`. Each carries a block
of `#ABC` directives, and the tests assert how `abc job run` translates those
into Nomad HCL — driver resolution, namespace, image, script templating, meta
and env passthrough.

These are **verbatim copies** of the scripts in
[`abc-cluster/abc-nodes`](https://github.com/abc-cluster/abc-nodes) at
`nomad/tests/workloads/`. They used to be read from `deployments/abc-nodes/`
in this repository, until commit `66471dc` extracted that tree into its own
repo and left the tests pointing at a path that no longer existed. Unit-test
fixtures should not depend on a sibling repository being checked out beside
this one, so they live here now.

Kept byte-identical to the originals so a drift check is a plain diff. If the
upstream scripts change in a way these tests should track, re-copy rather than
hand-edit.
