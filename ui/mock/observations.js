// Fixture for `VITE_MOCK=true npm run dev` (see vite.config.js), shaped
// like a real GET /api/observations response.
//
// Timestamps anchor to when this module loads (not a hardcoded date, and
// not recomputed per-request) so they stay sensible whenever the server
// starts, and so only the client's own clock tick advances "Completed X
// ago" between polls -- not the mock re-basing itself every 5s.
const bootedAt = Date.now();
const ago = (seconds) => new Date(bootedAt - seconds * 1000).toISOString();

const SCAN_DURATION_MS = 8420;
const startedAt = ago(SCAN_DURATION_MS / 1000 + 3); // scan started ~11.4s before boot
const scanId = startedAt;

// The two "stale" entries below are deliberately from an earlier scan, to
// exercise the (stale) marker driven by isFresh() in App.jsx.
const PREV_SCAN_OFFSET_S = 600;
const prevScanId = ago(PREV_SCAN_OFFSET_S + SCAN_DURATION_MS / 1000 + 3);

function classified(
  namespace,
  name,
  node,
  uid,
  offsetSeconds,
  classification,
  reasonCode,
  scanIdOverride,
) {
  const observedAt = ago(offsetSeconds);
  return {
    namespace,
    name,
    node,
    uid,
    updatedAt: observedAt,
    lastClassification: {
      namespace,
      name,
      node,
      uid,
      scanId: scanIdOverride ?? scanId,
      observedAt,
      outcome: "classified",
      classification,
      reasonCode,
    },
  };
}

function failed(namespace, name, node, uid, offsetSeconds, stage, errorCode) {
  const observedAt = ago(offsetSeconds);
  return {
    namespace,
    name,
    node,
    uid,
    updatedAt: observedAt,
    lastFailure: {
      namespace,
      name,
      node,
      uid,
      scanId,
      observedAt,
      outcome: "failed",
      stage,
      errorCode,
    },
  };
}

const OBSERVATIONS = [
  classified(
    "vdi",
    "win11-build-042",
    "worker-3",
    "3f1a8e2c-6b7d-4a3e-9c1f-2d4b8e6a1c90",
    8.4,
    "SUSPECTED_WINDOWS_BSOD",
    "WINDOWS_BSOD_VISIBLE",
  ),
  classified(
    "batch",
    "rhel-worker-03",
    "worker-1",
    "7c2d9f4a-1e5b-4c8a-8f3d-6a9c2e4b7d15",
    6.4,
    "SUSPECTED_KERNEL_PANIC",
    "KERNEL_PANIC_VISIBLE",
  ),
  classified(
    "vdi",
    "win10-vdi-014",
    "worker-2",
    "4b8e1d6a-2c9f-4e3b-8a1d-6c9e2b4a7d51",
    7.4,
    "SUSPECTED_WINDOWS_BSOD",
    "WINDOWS_BSOD_VISIBLE",
  ),
  classified(
    "batch",
    "rhel-app-22",
    "worker-3",
    "6d1a4c8e-9b2f-4d7a-a3c6-1e4b9a2d6f83",
    2.9,
    "SUSPECTED_KERNEL_PANIC",
    "KERNEL_PANIC_VISIBLE",
  ),
  failed(
    "vdi",
    "debian-app-07",
    "worker-2",
    "9a4e1c7b-3d6f-4b9a-a1c8-4e7b9d2f5a38",
    5.4,
    "SCREENSHOT",
    "TIMEOUT",
  ),
  failed(
    "batch",
    "suse-batch-05",
    "worker-1",
    "8f2c5a9d-3b6e-4c1f-9d4a-7b2e5c8f1a64",
    1.9,
    "CLASSIFIER",
    "MALFORMED_RESPONSE",
  ),
  classified(
    "batch",
    "centos-batch-02",
    "worker-3",
    "1d6b3a9e-4c7f-4e1b-9d3a-7c1e4b9a6d27",
    4.4,
    "UNKNOWN",
    "BLANK_OR_UNREADABLE",
  ),
  classified(
    "vdi",
    "fedora-qa-09",
    "worker-3",
    "2e9c6b3a-8d1f-4a5c-b7e2-4d8a1c6e9b37",
    1.4,
    "UNKNOWN",
    "AMBIGUOUS_OR_CROPPED",
  ),
  classified(
    "batch",
    "rhel-build-018",
    "worker-2",
    "5e8c2b4a-9d1f-4a6c-8b3e-1d9a4c7e2f60",
    3.4,
    "NO_TARGET_FAILURE_VISIBLE",
    "NO_FAILURE_VISIBLE",
  ),
  classified(
    "batch",
    "ubuntu-ci-11",
    "worker-1",
    "7b3e6d9a-1c4f-4b8e-a2d5-9c3a6e1b4d70",
    0.9,
    "NO_TARGET_FAILURE_VISIBLE",
    "NO_FAILURE_VISIBLE",
  ),
  classified(
    "vdi",
    "fedora-dev-01",
    "worker-1",
    "2b7d4e9a-6c1f-4d8b-a3e6-9c4a1e7d5b82",
    PREV_SCAN_OFFSET_S + 8.9,
    "NO_TARGET_FAILURE_VISIBLE",
    "NO_FAILURE_VISIBLE",
    prevScanId,
  ),
  classified(
    "vdi",
    "ubuntu-dev-03",
    "worker-2",
    "3c7a5e8d-4b1f-4c9e-8a3d-6e9c2b5a8f14",
    PREV_SCAN_OFFSET_S + 7.9,
    "NO_TARGET_FAILURE_VISIBLE",
    "NO_FAILURE_VISIBLE",
    prevScanId,
  ),
];

export function buildMockObservations() {
  const failedCount = OBSERVATIONS.filter((o) => o.lastFailure).length;
  const screenshotFailures = OBSERVATIONS.filter(
    (o) => o.lastFailure?.stage === "SCREENSHOT",
  ).length;
  return {
    lastScan: {
      scanId,
      startedAt,
      durationMs: SCAN_DURATION_MS,
      namespaces: 2,
      selected: OBSERVATIONS.length,
      captured: OBSERVATIONS.length - screenshotFailures,
      classified: OBSERVATIONS.length - failedCount,
      skipped: 0,
      errors: failedCount,
      overrun: false,
    },
    observations: OBSERVATIONS,
    triageEnabled: true,
  };
}

const TRIAGE_RESULT_FIXTURES = {
  "vdi/win11-build-042": {
    suspectedCause: "WINDOWS_BSOD",
    confidence: "HIGH",
    summary:
      'The current graphical console still shows a Windows "Blue Screen of Death" stop error. The persisted console log confirms the guest rebooted into automatic repair immediately beforehand, consistent with the automated classifier\'s finding.',
    keyEvidence: [
      "console_screenshot: stop code IRQL_NOT_LESS_OR_EQUAL still on screen",
      "console_log: guest logged a bugcheck and an automatic-repair boot entry moments before the screenshot",
    ],
    nextSteps: [
      "Check for a recently installed driver or Windows update around the bugcheck time",
      "Boot into WinRE to review the minidump if guest access is available",
    ],
    toolsUsed: ["console_screenshot", "console_log"],
    toolCallCount: 2,
    durationMs: 4200,
    usage: { inputTokens: 5210, outputTokens: 340, reasoningTokens: 780 },
  },
  "batch/rhel-worker-03": {
    suspectedCause: "STORAGE_FAILURE",
    confidence: "HIGH",
    summary:
      'The persisted console log shows a Linux kernel panic ("VFS: Unable to mount root fs on unknown-block(0,0)") shortly after boot, and the current screenshot still shows the same panic text. The root filesystem never became available, which points at the underlying storage rather than the kernel itself.',
    keyEvidence: [
      'console_log: "Kernel panic - not syncing: VFS: Unable to mount root fs on unknown-block(0,0)"',
      "console_screenshot confirms the panic text is still displayed",
    ],
    nextSteps: [
      "Check whether the VM's root disk PVC is still bound and healthy",
      "Review any recent change to the VM's boot disk, image, or kernel command line",
    ],
    toolsUsed: ["console_log", "console_screenshot"],
    toolCallCount: 2,
    durationMs: 3100,
    usage: { inputTokens: 4180, outputTokens: 312, reasoningTokens: 640 },
  },
  "vdi/win10-vdi-014": {
    suspectedCause: "BOOT_FAILURE",
    confidence: "MEDIUM",
    summary:
      "The graphical console shows a black screen with a single dialog reporting a missing boot configuration entry, rather than a full stop-error screen. console_log returned no data for this guest, so this assessment is based on the screenshot alone.",
    keyEvidence: [
      'console_screenshot: "Windows failed to start" boot-configuration error dialog',
      "console_log: SERIAL_DISABLED -- no persisted serial console configured for this guest",
    ],
    nextSteps: [
      "Verify the VM's boot order and EFI/BCD configuration",
      "Enable a serial console on this VM to capture more detail on the next failure",
    ],
    toolsUsed: ["console_screenshot", "console_log"],
    toolCallCount: 2,
    durationMs: 2600,
    usage: { inputTokens: 3120, outputTokens: 210, reasoningTokens: 410 },
  },
  "batch/rhel-app-22": {
    suspectedCause: "KERNEL_PANIC",
    confidence: "HIGH",
    summary:
      'A live capture of the serial console confirms the guest is still repeatedly printing the same oops trace ("Unable to handle kernel NULL pointer dereference") and has not progressed in the last 5 seconds of output. The persisted log shows the same trace starting shortly after boot.',
    keyEvidence: [
      "console_capture: guest is looping the same oops trace with no new output",
      'console_log: "Unable to handle kernel NULL pointer dereference at 0000000000000018"',
      "console_screenshot matches the captured trace",
    ],
    nextSteps: [
      "Capture and review the full oops trace for the faulting module or driver",
      "Roll back any kernel or module update applied to this image before the crash",
    ],
    toolsUsed: ["console_log", "console_capture", "console_screenshot"],
    toolCallCount: 4,
    durationMs: 6800,
    usage: { inputTokens: 7460, outputTokens: 455, reasoningTokens: 1120 },
  },
  "vdi/debian-app-07": {
    suspectedCause: "GUEST_HUNG",
    confidence: "MEDIUM",
    summary:
      "The scan's own screenshot attempt timed out, so no classification was ever made. A live capture now shows the console is active but has not printed any new output in 5 seconds, and the graphical console is static. This looks like a hung guest rather than a transient screenshot failure.",
    keyEvidence: [
      "console_capture: no new serial output over a 5-second window",
      "console_screenshot: cursor and screen contents unchanged from the previous poll",
    ],
    nextSteps: [
      "Check the guest's CPU and I/O wait metrics for signs of a stall",
      "Consider a graceful restart if the guest does not recover on its own",
    ],
    toolsUsed: ["console_capture", "console_screenshot"],
    toolCallCount: 2,
    durationMs: 5200,
    usage: { inputTokens: 4890, outputTokens: 298, reasoningTokens: 560 },
  },
  "batch/centos-batch-02": {
    suspectedCause: "OUT_OF_MEMORY",
    confidence: "MEDIUM",
    summary:
      "The scan's screenshot was blank, which matched the automated classifier's UNKNOWN result. The persisted console log shows the kernel's OOM killer terminated several processes just before the screen went blank, which explains the blank console better than a rendering glitch would.",
    keyEvidence: [
      'console_log: "Out of memory: Killed process ... (java)" repeated three times',
      "console_screenshot: still blank, consistent with the guest's display service having been killed",
    ],
    nextSteps: [
      "Review the guest's memory limits and recent workload for a leak or spike",
      "Consider raising the VM's memory allocation if this recurs",
    ],
    toolsUsed: ["console_log", "console_screenshot"],
    toolCallCount: 2,
    durationMs: 3400,
    usage: { inputTokens: 3980, outputTokens: 264, reasoningTokens: 520 },
  },
  "vdi/fedora-qa-09": {
    suspectedCause: "NO_FAILURE_FOUND",
    confidence: "HIGH",
    summary:
      "The classifier flagged this screenshot as ambiguous because the capture appears cropped, cutting off part of the screen. The persisted console log shows only routine login-prompt activity with no errors, and a fresh screenshot shows the same login prompt in full. This looks like a capture artifact, not a guest failure.",
    keyEvidence: [
      "console_log: routine systemd and login-prompt messages only, no errors",
      "console_screenshot (retaken): full login prompt visible, not cropped",
    ],
    nextSteps: [
      "No action needed for the guest; if cropped screenshots recur, check the console MCP server's capture geometry",
    ],
    toolsUsed: ["console_log", "console_screenshot"],
    toolCallCount: 2,
    durationMs: 2900,
    usage: { inputTokens: 3540, outputTokens: 238, reasoningTokens: 380 },
  },
  "batch/rhel-build-018": {
    suspectedCause: "NO_FAILURE_FOUND",
    confidence: "HIGH",
    summary:
      "Both the current screenshot and the persisted console log show the guest running normally at a shell prompt, with no error or crash text anywhere in the recent log. This confirms the automated classifier's finding.",
    keyEvidence: [
      "console_screenshot: guest at an interactive shell prompt",
      "console_log: no errors or warnings in the last 500 lines",
    ],
    nextSteps: ["No action needed"],
    toolsUsed: ["console_screenshot", "console_log"],
    toolCallCount: 2,
    durationMs: 2400,
    usage: { inputTokens: 3210, outputTokens: 190, reasoningTokens: 260 },
  },
  "batch/ubuntu-ci-11": {
    suspectedCause: "NO_FAILURE_FOUND",
    confidence: "MEDIUM",
    summary:
      "The current screenshot shows the guest at a normal login prompt. console_log could not be read for this guest, so this assessment is based on the screenshot alone.",
    keyEvidence: [
      "console_screenshot: normal login prompt, no visible errors",
      "console_log: SERIAL_DISABLED -- no persisted serial console configured for this guest",
    ],
    nextSteps: [
      "No action needed; enable a serial console here for higher-confidence checks in the future",
    ],
    toolsUsed: ["console_screenshot", "console_log"],
    toolCallCount: 2,
    durationMs: 2100,
    usage: { inputTokens: 2680, outputTokens: 175, reasoningTokens: 210 },
  },
  "vdi/fedora-dev-01": {
    suspectedCause: "INDETERMINATE",
    confidence: "LOW",
    summary:
      "The current screenshot came back blank and console_log returned no data for this guest. It is not possible to confirm whether the guest is healthy from this alone, and this VM's last classification is now over 10 minutes old.",
    keyEvidence: [
      "console_screenshot: blank output",
      "console_log: SERIAL_DISABLED -- no persisted serial console configured for this guest",
    ],
    nextSteps: [
      "Enable a serial console on this VM so a future investigation has more to go on",
      "Re-run the investigation after the next scan captures a fresh screenshot",
    ],
    toolsUsed: ["console_screenshot", "console_log"],
    toolCallCount: 2,
    durationMs: 1800,
    usage: { inputTokens: 2210, outputTokens: 140, reasoningTokens: 160 },
  },
  "vdi/ubuntu-dev-03": {
    suspectedCause: "NO_FAILURE_FOUND",
    confidence: "MEDIUM",
    summary:
      "The current screenshot shows the guest at a normal desktop session. The persisted console log is quiet, with no crash or panic text, though this VM's last classification is now over 10 minutes old.",
    keyEvidence: [
      "console_screenshot: normal desktop session",
      "console_log: no errors in the last 500 lines",
    ],
    nextSteps: ["No action needed"],
    toolsUsed: ["console_screenshot", "console_log"],
    toolCallCount: 2,
    durationMs: 2300,
    usage: { inputTokens: 2890, outputTokens: 182, reasoningTokens: 240 },
  },
};

const TRIAGE_ERROR_FIXTURES = {
  "batch/suse-batch-05": "TIMEOUT",
};

export function buildMockTriageRecord(namespace, name) {
  const key = `${namespace}/${name}`;
  const base = {
    namespace,
    name,
    uid: "mock-uid",
    requestedAt: new Date().toISOString(),
  };

  const errorCode = TRIAGE_ERROR_FIXTURES[key];
  if (errorCode) {
    return {
      ...base,
      durationMs: 90000,
      usage: { inputTokens: 6200, outputTokens: 0, reasoningTokens: 0 },
      errorCode,
    };
  }

  const { durationMs, usage, ...result } =
    TRIAGE_RESULT_FIXTURES[key] ??
    TRIAGE_RESULT_FIXTURES["batch/rhel-worker-03"];
  return { ...base, durationMs, usage, result };
}

OBSERVATIONS[0].lastTriage = buildMockTriageRecord(
  OBSERVATIONS[0].namespace,
  OBSERVATIONS[0].name,
);
