# ptpgen

A CLI tool that generates [PtpConfig](https://docs.openshift.com/container-platform/latest/networking/ptp/about-ptp.html) custom resources for OpenShift PTP Operator deployments. It discovers the cluster's network topology via L2 discovery, solves topology constraints for the requested PTP mode, and outputs ready-to-apply YAML.

## Overview

`ptpgen` automates what is otherwise a manual, error-prone process:

1. **Discovers** PTP-capable interfaces and LAN connectivity across cluster nodes using [l2discovery-lib](https://github.com/redhat-cne/l2discovery-lib)
2. **Solves** topology constraints to assign clock roles (grandmaster, boundary clock, ordinary clock, etc.) to the right nodes and interfaces
3. **Generates** one or more `PtpConfig` resources with correct ptp4l configuration, phc2sys options, interface assignments, and node label selectors

## Quick Start

### Prerequisites

- An OpenShift cluster with the [PTP Operator](https://docs.openshift.com/container-platform/latest/networking/ptp/about-ptp.html) installed
- `KUBECONFIG` set or `--kubeconfig` pointing to your cluster
- One or more nodes with PTP-capable NICs (single-node clusters with an external grandmaster are supported)

### 1. Preview the configs

Generate YAML for a Boundary Clock setup and review it:

```bash
go run main.go --mode bc
```

This runs L2 discovery on your cluster, solves the topology, and prints PtpConfig YAML to stdout.

### 2. Apply to the cluster

Once you're happy with the output, apply it directly:

```bash
go run main.go --mode bc --clean --apply
```

This will:
- Remove any existing test PtpConfigs and node labels
- Discover your cluster topology
- Label the appropriate nodes (`ptp/test-grandmaster`, `ptp/clock-under-test`)
- Create the PtpConfig resources

### 3. Verify

```bash
oc get ptpconfigs -n openshift-ptp
oc get nodes --show-labels | grep ptp/
```

### 4. Tear down

```bash
go run main.go --clean
```

### Common workflows

```bash
# Ordinary Clock with external grandmaster
go run main.go --mode oc --external-gm --apply

# Dual NIC Boundary Clock with HA and FIFO scheduling
go run main.go --mode dualnicbcha --fifo --clean --apply

# Save configs to file, edit, then apply manually
go run main.go --mode bc > ptpconfigs.yaml
# ... edit ptpconfigs.yaml ...
oc apply -f ptpconfigs.yaml

# Telco Grandmaster with WPC NIC
go run main.go --mode tgm --wpc-interfaces ens2f0,ens2f1 --wpc-device-id gnss0 --apply
```

## Supported PTP Modes

| Mode | Flag | Description |
|------|------|-------------|
| Ordinary Clock | `--mode oc` | Single slave clock syncing to a grandmaster |
| Boundary Clock | `--mode bc` | Node with one slave and one master port, relaying time |
| Dual NIC Boundary Clock | `--mode dualnicbc` | Two boundary clocks on separate NICs |
| Dual NIC BC with HA | `--mode dualnicbcha` | Dual NIC BC with phc2sys high-availability failover |
| Telco Grandmaster | `--mode tgm` | GNSS-based grandmaster using WPC (E810) NICs |
| Dual Follower | `--mode dualfollower` | One node with two slave ports on the same LAN |

Each mode generates the full set of PtpConfig resources needed (e.g., BC mode generates a grandmaster config + boundary clock config, and optionally a downstream slave config if the topology supports it).

## Installation

### From release packages

Download a pre-built binary from the [GitHub Releases](https://github.com/redhat-cne/ptpgen/releases) page:

```bash
# Linux (amd64)
curl -LO https://github.com/redhat-cne/ptpgen/releases/latest/download/ptpgen_Linux_x86_64.tar.gz
tar xzf ptpgen_Linux_x86_64.tar.gz ptpgen
sudo mv ptpgen /usr/local/bin/

# Linux (arm64)
curl -LO https://github.com/redhat-cne/ptpgen/releases/latest/download/ptpgen_Linux_arm64.tar.gz
tar xzf ptpgen_Linux_arm64.tar.gz ptpgen
sudo mv ptpgen /usr/local/bin/

# macOS (Apple Silicon)
curl -LO https://github.com/redhat-cne/ptpgen/releases/latest/download/ptpgen_Darwin_arm64.tar.gz
tar xzf ptpgen_Darwin_arm64.tar.gz ptpgen
sudo mv ptpgen /usr/local/bin/

# macOS (Intel)
curl -LO https://github.com/redhat-cne/ptpgen/releases/latest/download/ptpgen_Darwin_x86_64.tar.gz
tar xzf ptpgen_Darwin_x86_64.tar.gz ptpgen
sudo mv ptpgen /usr/local/bin/
```

### From source

```bash
git clone https://github.com/redhat-cne/ptpgen.git
cd ptpgen
go build -o ptpgen .

# Optionally install to $GOPATH/bin
cp ptpgen ~/go/bin/
```

Or run directly without building:

```bash
go run main.go --mode bc
```

## Usage

```bash
# Generate Boundary Clock configs and print YAML to stdout
go run main.go --mode bc --kubeconfig ~/.kube/config

# Generate OC configs with an external grandmaster
go run main.go --mode oc --external-gm

# Generate Dual NIC BC with HA, FIFO scheduling, and PTP auth
go run main.go --mode dualnicbcha --fifo --auth

# Pipe directly into oc/kubectl to apply
go run main.go --mode bc | oc apply -f -

# Save to a file for review
go run main.go --mode dualnicbc > ptpconfigs.yaml

# Apply directly: discover topology, label nodes, create PtpConfigs
go run main.go --mode bc --apply

# Clean existing test configs first, then apply new ones
go run main.go --mode bc --clean --apply

# Clean only (remove all test PtpConfigs and node labels)
go run main.go --clean
```

If you built the binary (`go build -o ptpgen .`), replace `go run main.go` with `./ptpgen` in the examples above.

## Options

```
  -mode string
        PTP mode: oc, bc, dualnicbc, dualnicbcha, tgm, dualfollower (required unless -clean)
  -kubeconfig string
        Path to kubeconfig (default: $KUBECONFIG, then in-cluster)
  -apply
        Apply configs to cluster (label nodes + create PtpConfigs)
  -clean
        Clean existing test PtpConfigs and node labels (can be used alone or with -apply)
  -external-gm
        Force external grandmaster mode (auto-detected if no internal GM solution exists)
  -fifo
        Use SCHED_FIFO scheduling policy (default: SCHED_OTHER)
  -auth
        Enable PTP authentication (NTS/MACsec)
  -namespace string
        PTP operator namespace (default "openshift-ptp")
  -domain int
        PTP domain number (default 24)
  -container-cmds
        Use container commands for L2 discovery (lspci, ethtool)
  -verbose
        Enable verbose logging
  -wpc-interfaces string
        Comma-separated WPC interface names (TGM mode only)
  -wpc-device-id string
        WPC GNSS device ID (TGM mode only)
```

## How It Works

### L2 Discovery

`ptpgen` uses the [l2discovery-lib](https://github.com/redhat-cne/l2discovery-lib) to:

- Deploy discovery pods on each node
- Detect PTP-capable network interfaces (via ethtool)
- Map LAN connectivity between nodes (which interfaces can reach each other at L2)
- Identify NIC topology (which ports share a PHC)
- Detect WPC-enabled NICs for Telco Grandmaster mode

### Constraint Solver

Each PTP mode maps to a set of topology constraints. For example, a Boundary Clock requires:

- A slave port and master port on the **same NIC** (same PHC)
- A grandmaster port on the **same LAN** as the BC's slave port

The solver finds valid assignments of cluster interfaces to these roles. If no internal grandmaster solution exists (e.g., single-node clusters) but external GM solutions are found, `ptpgen` automatically falls back to external GM mode.

### Config Generation

Once roles are assigned, `ptpgen` builds `PtpConfig` resources with:

- **ptp4l configuration** - full `ptp4l.conf` content with interface sections, domain, JBOD settings
- **ptp4l options** - daemon flags (`-2 --summary_interval -4`, `-s` for slave mode)
- **phc2sys options** - clock synchronization flags appropriate to the role
- **Node label selectors** - `ptp/test-grandmaster`, `ptp/clock-under-test`, `ptp/test-slave1`, etc.
- **Scheduling policy** - `SCHED_OTHER` or `SCHED_FIFO`

## Output

The tool outputs standard Kubernetes YAML, with multiple resources separated by `---`:

```yaml
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: test-grandmaster
  namespace: openshift-ptp
spec:
  profile:
    - name: test-grandmaster
      interface: ens2f0
      ptp4lOpts: "-2 --summary_interval -4"
      phc2sysOpts: "-a -r -r -n 24"
      ptp4lConf: |
        [global]
        domainNumber 24
        ...
        [ens2f0]
        masterOnly 1
  recommend:
    - profile: test-grandmaster
      priority: 5
      match:
        - nodeLabel: ptp/test-grandmaster
---
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: test-bc-master1
  namespace: openshift-ptp
spec:
  ...
```

## Node Labels

The generated configs use node label selectors for scheduling. With `--apply`, nodes are labeled automatically based on the discovered topology. For manual (YAML) mode, label your nodes before applying:

```bash
oc label node <gm-node> ptp/test-grandmaster=
oc label node <bc-node> ptp/clock-under-test=
oc label node <slave-node> ptp/test-slave1=
```

## Apply Mode

With `--apply`, ptpgen performs the full setup workflow:

1. **Clean** (if `--clean` is also set) - deletes existing test PtpConfigs and removes node labels
2. **Label nodes** - applies the appropriate labels (`ptp/test-grandmaster`, `ptp/clock-under-test`, etc.) to nodes based on the discovered topology
3. **Create PtpConfigs** - creates the generated PtpConfig resources on the cluster

This mirrors what the ptp-operator test suite does in `CreatePtpConfigurations()`.

Use `--clean` alone to tear down a previous configuration:

```bash
go run main.go --clean
```

## Project Structure

```
ptpgen/
├── main.go                      # CLI entry point
├── pkg/
│   ├── client/client.go         # Kubernetes client setup
│   ├── cluster/cluster.go       # Clean, label nodes, apply configs
│   ├── config/
│   │   ├── base.go              # ptp4l/ts2phc config templates
│   │   └── config.go            # PtpConfig builders for all modes
│   └── discovery/discovery.go   # L2 discovery + constraint solver
├── go.mod
└── go.sum
```

## Related Projects

- [ptp-operator](https://github.com/k8snetworkplumbingwg/ptp-operator) - OpenShift PTP Operator
- [l2discovery-lib](https://github.com/redhat-cne/l2discovery-lib) - L2 network discovery library
- [linuxptp-daemon](https://github.com/k8snetworkplumbingwg/linuxptp-daemon) - PTP daemon for Kubernetes
- [cloud-event-proxy](https://github.com/redhat-cne/cloud-event-proxy) - PTP event notification framework
