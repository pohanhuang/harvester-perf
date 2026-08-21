# BenchCTL

BenchCTL is a small Harvester benchmark POC built on top of ClusterLoader2.

ClusterLoader2 project:

- https://github.com/kubernetes/perf-tests/tree/master/clusterloader2

## What Exists Today

- A CLI entrypoint in `cmd/main.go`
- A thin ClusterLoader2 adapter in `cl2/`
- A scenario layer for Harvester and KubeVirt workflows in `scenarios/`
- A custom measurement layer in `measurements/`
- Built-in YAML scenario loading for selected benchmarks
- Result writing as text and JSON summaries

## Result Folders

Current result will output as a folder, but within the pod. In current repo, you can check `results/`

Currently, PoC provides

- platform baseline
- vm creation time
- vm capacity

Aline to Phase 1 in the [v1.7.1 Performance benchmark report](https://gist.github.com/pohanhuang/8a17d314b5fbd6b9833f43598e580d84#phase-1-idle-baseline-0-vms)

## How To Run

Today the simplest way to run the POC is as an in-cluster pod.

Required manifests:

- [rbac.yaml](/home/po/harvester/harvester-perf/rbac.yaml)
- [pod.yaml](/home/po/harvester/harvester-perf/pod.yaml)

Typical flow:

1. Build the BenchCTL image.
2. Make the image available to your cluster.
3. Apply the RBAC manifest.
4. Apply the pod manifest.
5. Read the pod logs.

Example:

```bash
make build
kubectl apply -f rbac.yaml
kubectl apply -f pod.yaml
kubectl logs -f pod/benchctl-hello
```

At the current POC stage, this is enough to execute the benchmark container. In practice, the main setup work is:

- defining the pod entrypoint and arguments in `pod.yaml`
- granting the needed cluster permissions in `rbac.yaml`

If you want to run a different scenario, the usual change is to update the container arguments in [pod.yaml](/home/po/harvester/harvester-perf/pod.yaml:1).

## Current Scope

This repo is not a full benchmark platform yet. The current code proves that we can:

- Reuse ClusterLoader2 framework and measurement manager
- Keep Harvester-specific workflows in custom scenarios
- Load simple scenario definitions from YAML
- Run either:
  - a native BenchCTL scenario, or
  - a ClusterLoader2 config through the local adapter

## Implemented Scenarios

- `platform-baseline`
  - Collects cluster resource usage summaries
- `harvester-vm-create`
  - Creates KubeVirt VMs and reports API-create and running latency
- `harvester-vm-capacity`
  - Creates VMs in batches and stops when the cluster can no longer keep up

There are also small demo scenarios such as `hello` and `resource-usage`.

## Config Model

The repo already supports a lightweight YAML scenario format:

```yaml
name: harvester-vm-create
steps:
  - name: prepare-image
    config:
      mode: containerDisk
      image: quay.io/kubevirt/cirros-container-disk-demo:latest
  - name: create-vms
    config:
      namespace: default
      count: 5
  - name: wait-running
    config:
      timeout: 20m
      pollInterval: 5s
  - name: cleanup-vms
    config:
      enabled: true
```

This is enough for the current POC, but it is still intentionally simple.

## How It Is Structured

The current design is:

```text
BenchCTL = Harvester/KubeVirt scenario layer
         + ClusterLoader2 framework and measurement layer
```

In practice:

- Scenarios own the workflow
- Measurements own observation and summaries
- ClusterLoader2 provides framework plumbing and reusable measurement machinery

## Why This Matters

This POC shows that Harvester benchmarks do not need to fit entirely into native ClusterLoader2 object phases.

We can keep imperative workflows, such as VM lifecycle tests, in Go scenarios while still reusing ClusterLoader2 where it is strong:

- framework setup
- client handling
- measurement registration
- summary interfaces

## Likely Next Step

The next practical extension is to support script-backed scenarios and richer measurements for:

- Prometheus-based resource and latency collection
- `fio`
- `iperf3`
- `etcd benchmark`
- existing custom Bash workflows such as CSI stress tests

## Reference

- [HEP](https://github.com/harvester/harvester/blob/master/enhancements/20260424-harvester-benchmark-policy.md) describes the benchmark policy this POC is trying to support.
