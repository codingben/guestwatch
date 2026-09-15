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

// Edit this directly to try different fleet sizes/mixes; lastScan's counts
// below derive from it, so they stay in sync automatically.
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
  };
}
