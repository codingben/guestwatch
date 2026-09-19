# GuestWatch

_"Is any of these VMs sitting at a kernel panic or a Windows BSOD right
now?"_

---

An autonomous AI monitoring agent for [KubeVirt](https://kubevirt.io). It
periodically captures a screenshot of each watched VM's graphical console,
asks a vision model whether the guest operating system has crashed, and logs
a structured finding when it has — with no user prompt, or manual triage
step required.

## Workflow

Each scan interval, the agent runs this pipeline:

1. **Discover** — list `VirtualMachineInstance` in the configured
   namespaces (optionally filtered by a label selector) and keep the ones
   that are [eligible](internal/domain/types.go) for a screenshot: running,
   scheduled to a node, not paused, not mid-migration, and not configured
   with graphics device autoattach disabled.
2. **Capture** — for each eligible VM, call the `console_screenshot` tool on
   a [Console MCP](https://modelcontextprotocol.io) server, which returns a
   PNG frame of the console framebuffer. Immediately before capture, the
   agent re-fetches the VMI and re-checks its UID and eligibility, so a VM
   deleted or recreated between discovery and capture is skipped rather than
   misattributed.
3. **Classify** — send the screenshot to a vision model with a fixed prompt
   and a strict JSON schema. The model returns exactly one classification
   (`NO_TARGET_FAILURE_VISIBLE`, `SUSPECTED_KERNEL_PANIC`,
   `SUSPECTED_WINDOWS_BSOD`, or `UNKNOWN`) and one matching reason code — no
   free-form text, no confidence score, no tool calls.
4. **Report** — suspected findings and coverage gaps (namespaces or VMs the
   scan couldn't cover, with a bounded error code) are logged as structured
   JSON. A `scan_complete` summary line closes out every scan with counts
   and token usage.

### Scan Configuration

```yaml
scan:
  namespaces: [payments, checkout, shared-services]
  labelSelector: "monitoring.kubevirt.io/critical=true"
  interval: 5m
  perNodeConcurrency: 1
  classifierRPS: 2
  classifierConcurrency: 5
  firstPassDeadline: 45s
mcp:
  consoleURL: "https://kubevirt-console-mcp:8443/mcp"
model:
  classifier: "your-vision-model-id"
privacy:
  consoleEvidenceEgressAcknowledged: true
```

## Triage

Triage investigates a VM using a reasoning model and the [KubeVirt Console MCP](https://github.com/codingben/kubevirt-console-mcp)'s
read-only tools:

- `console_screenshot` — graphical console.
- `console_log` — persisted serial-console output.
- `console_capture` — live serial-console output (≤30s).

### Triage Configuration

```yaml
triage:
  enabled: true
  model: "your-reasoning-model-id"
  maxToolCalls: 8
  deadline: 90s
  rps: 0.5
  concurrency: 2
privacy:
  triageEvidenceEgressAcknowledged: true
```

## License

Released under the [Apache License 2.0](LICENSE).
