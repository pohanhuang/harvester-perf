# harvester-perf

This repository contains a collection of tools, pipelines and documentation for
assessing Harvester performance and benchmark results.

`hvperf` is a CLI for running performance, capacity and benchmark test suites
against [SUSE Harvester](https://harvesterhci.io) clusters.

Suites are driven from your workstation using an ordinary kubeconfig. Nothing is
installed on the Harvester nodes; suites that need on-node access (such as the
etcd benchmark) schedule a short-lived helper job in a namespace of their own,
which `--keep-alive=false` tears down once the run is over.

These helper pods run **privileged**, since the on-node access they need can't
be had otherwise: e.g., the etcd benchmark's job pod mounts `/var/lib/rancher` to
reach the RKE2 etcd TLS certs, and `node-capacity`'s daemonset pod uses `hostPID`
to run `nsenter`/`lsblk` against the node's disks.

> **Status:** early development. The suite registry, CLI and etcd job plumbing
> are in place; individual suites are still being filled in. See
> [Test suites](#test-suites) for what each one currently does.

## Quick start

```bash
# build ./bin/hvperf (runs the compiler inside a container, see Building)
make go/build

# what can be run
./bin/hvperf list

# client and cluster versions
./bin/hvperf version

# run a single suite
./bin/hvperf run node-capacity

# run several suites (one comma-separated argument, not a space-separated list)
./bin/hvperf run node-capacity,etcd-benchmark

# run every registered suite
./bin/hvperf run all

# run with a custom config file (default: ./hvperf.yaml)
./bin/hvperf run density --config /path/to/hvperf.yaml
```

`hvperf run` reads `./hvperf.yaml` by default. Copy and edit the file at the
repo root to tune suite parameters before running:

```yaml
density:
  batchWaitTimeout: 5m     # per-batch creation timeout

  vmImage:
    url: "https://download.cirros-cloud.net/0.6.2/cirros-0.6.2-x86_64-disk.img"

  vm:
    storageClass: longhorn
    diskSize: 1Gi
    memory: 90Mi
    cpu: 100m
```

Pass `--config` to point at a different file:

```bash
./bin/hvperf run density --config ~/my-cluster.yaml
```

Every command that talks to a cluster accepts the standard kubectl connection
flags (`--kubeconfig`, `--context`, `--namespace`, `--server`, `--as`, …), so
targeting a specific cluster works the same way it does with `kubectl`:

```bash
./bin/hvperf run all --kubeconfig ~/.kube/harvester.yaml --context prod
```

Suites that create resources put them in the `harvester-perf-system` namespace,
creating the namespace if it does not exist. Pass `--namespace` to use a
different one.

The Dockerfile builds a container image with `hvperf` and the tools the suites
need to run in the cluster. For example, running the etcd suite from a locally
built binary rather than the image will fail unless `etcdctl`, `benchmark` and
`promtool` are present at `/usr/local/bin`. See
[Container image](#container-image) for how to build and run the image.

## Commands

| Command | Description |
| --- | --- |
| `hvperf list` | List registered suites. `-o` accepts `table` (default), `name`, `json`, `yaml`. |
| `hvperf run <suite>[,<suite>...]` | Run the named suites, given as a single comma-separated argument. Unknown names are ignored. `-o` accepts `text` (default), `json`, `yaml`. |
| `hvperf run all` | Run every registered suite, read-only and read-write alike. |
| `hvperf version [--client-only]` | Print the client version and, unless `--client-only` is set, the cluster's server version. |
| `hvperf report` | Placeholder — not implemented yet. |

`run` also takes `--config` (default `./hvperf.yaml`) to point at a config file
for suites that read it (currently `density`).

`run` also takes `--keep-alive` (default `true`), which decides what happens to
the test namespace once the run finishes. With `--keep-alive=false` the
namespace and everything in it is deleted afterwards — but only when it is the
default `harvester-perf-system`.

Results are written to stdout; suite progress is logged to stderr, so
`hvperf run ... -o json > results.json` keeps the two streams separate. Pass
`-q`/`--quiet` to silence the progress logging, or `-v <level>` to raise it.
Results are printed even when a suite fails, so partial output is not lost.

## Test suites

Suites are either **read-only** — they only query the API server — or
**read-write**, meaning they create or modify cluster resources. The mode is
reported by `hvperf list`; it does not filter what `hvperf run all` runs, which
is every registered suite.

| Suite | Mode | What it does |
| --- | --- | --- |
| `node-capacity` | read-write | Assess node resource capacity: per-node OS info and disk info via a privileged `hostPID` daemonset. |
| `etcd-benchmark` | read-write | Exercises the cluster's etcd from a privileged `hostNetwork` job pod. |
| `resource-footprint` | read-only | Measures the cluster's resource footprint: per-namespace CPU/memory usage and requests, host memory, and node allocatable, via Prometheus queries. |
| `density` | read-write | Import a VM image, ramp up VMs concurrently until failure, and record VMI boot-latency percentiles (p50/p95/p99). |

## Result types

A `SuiteResult` holds one `CaseResult` per check the suite ran. Each
`CaseResult` records what it did in whichever of these it produced —
a case can populate more than one:

| Type | Use it for |
| --- | --- |
| `CmdResult` | The result of executing a command in a test case — e.g. `exec`-ing into a helper pod. `Stdout`/`Stderr` hold the command's output streams. |
| `MetricResult` | The result of a Prometheus query run in a test case — e.g. reading etcd metrics after a benchmark. `Samples` holds the returned vector, `Warnings` any promclient warnings. |
| `K8sResourceResult` | The result of querying Kubernetes/node resources in a test case — e.g. `node-capacity`'s per-node OS and disk info. `Data` is a flat map of the resource's spec/status attributes. |

### Errors

Every result type carries its own `Err`, at a different scope:

| Field | Scope |
| --- | --- |
| `SuiteResult.Err` | The suite itself failed outside any single case — e.g. setup or cleanup shared by the whole suite. |
| `CaseResult.Err` | The case failed outside a specific command/query — e.g. a namespace or daemonset never became ready. Setting it marks the case errored immediately, before its `CmdResults`/`MetricResults`/`K8sResourceResults` are even considered. |
| `CmdResult.Err` | One `exec`'d command failed. |
| `MetricResult.Err` | One Prometheus query failed. |
| `K8sResourceResult.Err` | One resource query failed (this is the one `error`-typed field; the others are `string`). |

`CaseResult.FinalizeState()` rolls all of the above up into the case's
`State`: any non-empty `Err`, or any error in `CmdResults`, `MetricResults`,
or `K8sResourceResults`, marks the case `CaseResultStateErrored`.
`NewCaseResult` and the `With*` builders call it for you.

## Building

The Makefile runs the Go toolchain inside the SUSE BCI golang image, so Docker
is the only hard prerequisite. Host `GOCACHE`/`GOMODCACHE` are bind-mounted into
the build container and output is written back as your own user, so no `sudo
chown` dance afterwards.

```bash
make go/build     # compile ./bin/hvperf
make go/test      # go test -cover -race -shuffle=on ./...
make go/tidy      # go mod tidy
make clean        # remove ./bin
```

Useful overrides:

| Variable | Default | Purpose |
| --- | --- | --- |
| `GOOS` / `GOARCH` | `linux` / `amd64` | Cross-compilation target for `go/build` |
| `HARVESTER_VERSION` | `1.9` | Version prefix; `CLI_VERSION` becomes `<version>+<short-sha>` |
| `BUILD_IMAGE_TAG` | `1.26` | Tag of `registry.suse.com/bci/golang` used to build and test |
| `DOCKER` | `docker` | Container runtime (e.g. `DOCKER=podman`) |

```bash
GOOS=darwin GOARCH=arm64 make go/build
```

To build natively instead, Go 1.26+ works directly: `go build -o bin/hvperf .`

## Container image

The image bundles `hvperf` with the tools the suites ship into the cluster:
upstream `etcdctl` and `benchmark`, built from the etcd source at the version
pinned in the Dockerfile (`ETCD_VERSION`, currently `v3.6.14`), and `promtool`
from the Prometheus release tarball (`PROMETHEUS_VERSION`, currently `3.14.0`).
The runtime base is `registry.suse.com/bci/bci-base` at `BCI_TAG` (`16.0`).

```bash
make image/build                                    # -> hvperf:<version>-<sha>
make image/cmd                                      # mounts ~/.kube read-only
make image/cmd IMAGE_CMD_ARGS="list"
make image/cmd IMAGE_CMD_ARGS="run etcd-benchmark"
make image/run_all                                  # shorthand for "run all"
```

The image defaults to `hvperf version`, and expects a kubeconfig at
`/root/.kube/config` — which is where the `image/*` targets bind-mount `~/.kube`.

Overrides: `IMAGE_NAME`, `IMAGE_TAG`, `IMAGE_PLATFORMS` (default
`linux/amd64`), `IMAGE_OUTPUT_TYPE`.

## Repository layout

```
.
├── main.go                # entry point; blank-imports internal/suites to register built-ins
├── cmd/                   # cobra command tree (root, list, run, report, version) and k8s client setup
├── pkg/suites/            # public API: Suite interface, registry, options, results, marshalling
├── pkg/k8s/               # cluster helpers the suites share: namespaces, jobs, exec, logs, pod monitors
├── internal/suites/
│   ├── etcd/              # etcd-benchmark suite
│   ├── nodes/             # node-capacity suite
│   └── options/           # decodes the generic pkg/suites.Options into a suite's own options struct
├── poc/                   # shell-based prototype this CLI is being ported from
├── METRICS.md             # the metrics the suites collect and how to read them
├── Dockerfile             # multi-stage: etcd tools + promtool + hvperf -> BCI base runtime
└── Makefile               # containerised build, test and image targets
```

## Adding a suite

1. Create a package under `internal/suites/<name>/`.
2. Implement `pkg/suites.Suite`:

   ```go
   type MySuite struct {
       pkgsuites.SuiteMarshaler
       *pkgsuites.Clients
   }

   func (s *MySuite) Name() string        { return "my-suite" }
   func (s *MySuite) Description() string { return "what it measures" }
   func (s *MySuite) IsReadWrite() bool   { return false }
   func (s *MySuite) RunE(ctx context.Context, runID, namespace string, opts pkgsuites.Options) pkgsuites.SuiteResult
   func (s *MySuite) SetClients(c *pkgsuites.Clients)
   ```

   Embedding `SuiteMarshaler` and setting `s.Marshal = s` at construction gives
   the suite its `list` table row and its JSON/YAML form for free.

   `runID` is generated once per `hvperf run` invocation and shared by every
   suite in it — use it to name and label anything the suite creates, and echo
   it back in the `SuiteResult`. `namespace` is where those resources belong.
   Record each check as a `CaseResult`; `Objects` is rendered as
   `(Kind) namespace/name` in the text output, and a case that could not be run
   for an environmental reason should set `State` to `CaseResultStateSkipped`
   instead of erroring. `NewCaseResult`/`NewCaseResultSkipped` build a
   `CaseResult` and finalize its `State` for you.

3. Give the suite an options struct. Settings shared across suites — the default
   namespace, job pod image and timeouts, the monitoring addon coordinates —
   live in `pkgsuites.DefaultGlobalOptions()`; decode them into your own struct
   with `internal/suites/options.FromOptions[*MyOptions]` and layer the
   suite-specific defaults on top. Passing that struct through
   `pkgsuites.ToSuiteParams` into `SuiteResult.Params` records the effective
   configuration in the results.

4. Register it from the package's `init()`:

   ```go
   func init() { pkgsuites.Register(NewMySuite()) }
   ```

5. Blank-import the package from `internal/suites/register.go` so `main` pulls
   it in.

Names are the registry key — two suites with the same `Name()` silently
overwrite each other. Suites should honour `ctx` cancellation and clean up
anything they create in the cluster.

## Proof of concept

`poc/` holds the shell implementation that preceded this CLI: a container that
runs `cluster-info`, `node-capacity`, `etcd-benchmark`, `controlplane-resources`
and `vm-density` suites and renders a markdown report. It covers more ground
than the Go CLI does today and is kept as a reference for the suites still to be
ported. See [`poc/README.md`](poc/README.md).

### reference

* kubevirt-benchmark
* kubevirt-observability-controller

## Requirements

* A kubeconfig with cluster-admin on the target Harvester cluster.
* Docker (or a compatible runtime) for the build and image targets.
* Go 1.26+ only if you build outside the container.
* The `rancher-monitoring` addon, only for the metrics cases; suites skip those
  cases when it is not enabled.
* For `etcd-benchmark`'s metrics cases, the RKE2 server config
  [`etcd-expose-metrics`](https://docs.rke2.io/reference/server_config#database)
  must be set to `true`; etcd does not expose metrics otherwise.

## License

Apache 2.0 — see [LICENSE](LICENSE).
