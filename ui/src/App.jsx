import { useEffect, useMemo, useState } from 'react';

const POLL_INTERVAL_MS = 5000;

// Mirrors internal/domain.Classification.
const CLASSIFICATION_STATUS = {
  SUSPECTED_KERNEL_PANIC: { label: 'Suspected kernel panic', tone: 'red', rank: 0, suspected: true },
  SUSPECTED_WINDOWS_BSOD: { label: 'Suspected Windows BSOD', tone: 'red', rank: 0, suspected: true },
  UNKNOWN: { label: 'Unknown', tone: 'gray', rank: 2, suspected: false },
  NO_TARGET_FAILURE_VISIBLE: { label: 'Healthy', tone: 'green', rank: 3, suspected: false },
};
const FAILURE_STATUS = { label: 'Coverage gap', tone: 'amber', rank: 1, suspected: false };

// Mirrors internal/domain.ReasonCode.
const REASON_TEXT = {
  NO_FAILURE_VISIBLE: 'No failure is visible on the console.',
  KERNEL_PANIC_VISIBLE: 'Kernel panic text is visible on the console.',
  WINDOWS_BSOD_VISIBLE: 'A Windows blue screen is visible on the console.',
  BLANK_OR_UNREADABLE: 'The console capture was blank or unreadable.',
  FIRMWARE_INSTALLER_RECOVERY: 'The console shows firmware, installer, or recovery output, not the guest OS.',
  AMBIGUOUS_OR_CROPPED: 'The console capture was ambiguous or cropped.',
  TEXT_TOO_SMALL: 'Console text was too small to read reliably.',
};

// Mirrors internal/domain.ErrorCode.
const STAGE_TEXT = { SCREENSHOT: 'capturing the console', CLASSIFIER: 'classifying the console' };
const ERROR_TEXT = {
  STALE_TARGET: 'the VM changed or disappeared before it could be checked.',
  PERMISSION_DENIED: 'the agent lacks permission to read this VM.',
  TIMEOUT: 'the check did not complete before its deadline.',
  SERVICE_UNAVAILABLE: 'the console service was unavailable.',
  MALFORMED_RESPONSE: 'the console service returned an unexpected response.',
  EVIDENCE_LIMIT: 'the screenshot exceeded the configured size limit.',
  LIST_EXPIRED: 'the list of VMs expired before it could be fully read.',
};

function timeAgo(date) {
  const seconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  if (seconds < 5) return 'just now';
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return date.toLocaleString();
}

async function fetchObservations(signal) {
  const res = await fetch('/api/observations', { signal });
  if (!res.ok) throw new Error(`dashboard API returned ${res.status}`);
  return res.json();
}

// A failed poll keeps previously loaded data and only sets `error`, so a
// transient blip shows a banner instead of blanking the dashboard.
function useObservations(pollMs) {
  const [state, setState] = useState({ data: null, error: null, loading: true, lastUpdatedAt: null });

  useEffect(() => {
    let cancelled = false;
    let timer;
    const controller = new AbortController();

    async function poll() {
      try {
        const data = await fetchObservations(controller.signal);
        if (cancelled) return;
        setState({ data, error: null, loading: false, lastUpdatedAt: new Date() });
      } catch (err) {
        if (cancelled || err.name === 'AbortError') return;
        setState(prev => ({ ...prev, error: err, loading: false }));
      } finally {
        if (!cancelled) timer = setTimeout(poll, pollMs);
      }
    }

    poll();
    return () => {
      cancelled = true;
      controller.abort();
      clearTimeout(timer);
    };
  }, [pollMs]);

  return state;
}

const CLOCK_TICK_MS = 1000;

// Forces a re-render every intervalMs so relative-time text keeps
// advancing between polls.
function useClockTick(intervalMs) {
  const [, forceRender] = useState(0);
  useEffect(() => {
    const id = setInterval(() => forceRender(n => n + 1), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
}

// entry.lastClassification and .lastFailure are independent (see
// internal/dashboard.Entry); "current" is whichever has the later observedAt.
function pickCurrent(entry) {
  const { lastClassification: c, lastFailure: f } = entry;
  if (c && f) {
    return new Date(f.observedAt) > new Date(c.observedAt)
      ? { record: f, kind: 'failed' }
      : { record: c, kind: 'classified' };
  }
  if (c) return { record: c, kind: 'classified' };
  if (f) return { record: f, kind: 'failed' };
  return null;
}

function statusOf(current) {
  if (current.kind === 'classified') {
    return CLASSIFICATION_STATUS[current.record.classification] ?? CLASSIFICATION_STATUS.UNKNOWN;
  }
  return FAILURE_STATUS;
}

function findingText(current) {
  if (current.kind === 'classified') {
    return REASON_TEXT[current.record.reasonCode] ?? 'No further detail is available for this classification.';
  }
  const stage = STAGE_TEXT[current.record.stage] ?? 'checking this VM';
  const reason = ERROR_TEXT[current.record.errorCode] ?? 'an unspecified error occurred.';
  return `Failed while ${stage}: ${reason}`;
}

// Not a scanId equality check: lastScan only updates after a scan fully
// completes (internal/agent.Scanner.scanOnce's defer), so mid-scan it still
// names the *previous* scan. Comparing observedAt to lastScan.startedAt
// instead keeps already-rechecked VMs "fresh" during that window too.
function isFresh(record, lastScan) {
  if (!lastScan) return true;
  return new Date(record.observedAt) >= new Date(lastScan.startedAt);
}

function useViewModel(data) {
  return useMemo(() => {
    if (!data) return [];
    return data.observations
      .map(entry => {
        const current = pickCurrent(entry);
        if (!current) return null;
        return {
          key: `${entry.namespace}/${entry.name}`,
          entry,
          current,
          status: statusOf(current),
          isFromLatestScan: isFresh(current.record, data.lastScan),
        };
      })
      .filter(Boolean)
      .sort((a, b) =>
        a.status.rank - b.status.rank ||
        a.entry.namespace.localeCompare(b.entry.namespace) ||
        a.entry.name.localeCompare(b.entry.name)
      );
  }, [data]);
}

function MonitorIcon() {
  return <img className="brand-icon" src="/icon.svg" alt="" aria-hidden="true" />;
}

const TONE_ICON = {
  red: (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <path
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinejoin="round"
        strokeLinecap="round"
        d="M12 3.5 21.5 20h-19L12 3.5Z"
      />
      <path stroke="currentColor" strokeWidth="2" strokeLinecap="round" d="M12 10v4.5" />
      <circle cx="12" cy="17.5" r="1.1" fill="currentColor" stroke="none" />
    </svg>
  ),
  amber: (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="2" />
      <path stroke="currentColor" strokeWidth="2" strokeLinecap="round" d="M12 7.5v5.5" />
      <circle cx="12" cy="16.2" r="1.1" fill="currentColor" stroke="none" />
    </svg>
  ),
  green: (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="2" />
      <path fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" d="m8 12.5 2.5 2.5L16 9.5" />
    </svg>
  ),
  gray: (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="2" />
      <path fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" d="M9.5 9.3a2.5 2.5 0 1 1 3.4 2.3c-.75.3-1.15.9-1.15 1.65v.3" />
      <circle cx="12" cy="16.7" r="1.05" fill="currentColor" stroke="none" />
    </svg>
  ),
};

function NodeIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <rect x="3.5" y="4.5" width="17" height="6" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <rect x="3.5" y="13.5" width="17" height="6" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <circle cx="7" cy="7.5" r="0.9" fill="currentColor" />
      <circle cx="7" cy="16.5" r="0.9" fill="currentColor" />
    </svg>
  );
}

function TagIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
        d="M11.5 4h-6A1.5 1.5 0 0 0 4 5.5v6c0 .4.16.78.44 1.06l8 8a1.5 1.5 0 0 0 2.12 0l6-6a1.5 1.5 0 0 0 0-2.12l-8-8A1.5 1.5 0 0 0 11.5 4Z"
      />
      <circle cx="8.5" cy="8.5" r="1.4" fill="currentColor" stroke="none" />
    </svg>
  );
}

function ClockIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <circle cx="12" cy="12" r="8.5" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <path fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" d="M12 7.5V12l3.2 2" />
    </svg>
  );
}

function ScanIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <path fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" d="M4 8V5.5A1.5 1.5 0 0 1 5.5 4H8M16 4h2.5A1.5 1.5 0 0 1 20 5.5V8M20 16v2.5a1.5 1.5 0 0 1-1.5 1.5H16M8 20H5.5A1.5 1.5 0 0 1 4 18.5V16" />
      <circle cx="12" cy="12" r="3" fill="none" stroke="currentColor" strokeWidth="1.6" />
    </svg>
  );
}

// Unlike the toolbar's "N suspected" headline, these match regardless of
// scan freshness: a stale suspected finding is still worth triaging.
const FILTERS = [
  { key: 'all', label: 'All', emptyText: 'No virtual machines match this filter.', predicate: () => true },
  { key: 'suspected', label: 'Suspected', emptyText: 'No suspected VMs right now.', predicate: vm => vm.status.suspected },
  { key: 'errors', label: 'Errors', emptyText: 'No VMs in error right now.', predicate: vm => vm.current.kind === 'failed' },
];

function FilterRow({ vms, activeKey, onChange }) {
  return (
    <div className="filter-row" role="group" aria-label="Filter virtual machines by status">
      {FILTERS.map(f => (
        <button
          key={f.key}
          type="button"
          className="filter-button"
          aria-pressed={f.key === activeKey}
          onClick={() => onChange(f.key)}
        >
          {f.label} ({vms.filter(f.predicate).length})
        </button>
      ))}
    </div>
  );
}

function SearchIcon() {
  return (
    <svg className="search-icon" viewBox="0 0 20 20" width="15" height="15" aria-hidden="true">
      <path
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        d="M13.5 13.5 17 17M9 15a6 6 0 1 1 0-12 6 6 0 0 1 0 12Z"
      />
    </svg>
  );
}

function SearchBox({ value, onChange }) {
  return (
    <div className="search-box">
      <SearchIcon />
      <input
        type="search"
        className="search-input"
        placeholder="Search by namespace or name…"
        value={value}
        onChange={e => onChange(e.target.value)}
        aria-label="Search virtual machines by namespace or name"
      />
      {value !== '' && (
        <button type="button" className="search-clear" aria-label="Clear search" onClick={() => onChange('')}>
          ✕
        </button>
      )}
    </div>
  );
}

function ScanSummary({ lastScan, suspectedNow }) {
  if (!lastScan) {
    return <span className="scan-waiting">Waiting for the first scan to complete…</span>;
  }
  const completedAt = new Date(new Date(lastScan.startedAt).getTime() + lastScan.durationMs);
  return (
    <div className="scan-summary">
      <span className="status-badge gray">
        {lastScan.selected} VM{lastScan.selected === 1 ? '' : 's'} scanned
      </span>
      {suspectedNow > 0 && <span className="status-badge red">{suspectedNow} suspected</span>}
      {lastScan.errors > 0 && (
        <span className="status-badge amber">
          {lastScan.errors} error{lastScan.errors === 1 ? '' : 's'}
        </span>
      )}
      {lastScan.overrun && <span className="status-badge amber">Running behind schedule</span>}
      <span className="scan-summary-time">Completed {timeAgo(completedAt)}</span>
    </div>
  );
}

export default function App() {
  useClockTick(CLOCK_TICK_MS);
  const { data, error, loading, lastUpdatedAt } = useObservations(POLL_INTERVAL_MS);
  const vms = useViewModel(data);
  const [selectedKey, setSelectedKey] = useState(null);
  const [filterKey, setFilterKey] = useState('all');
  const [query, setQuery] = useState('');

  const activeFilter = FILTERS.find(f => f.key === filterKey) ?? FILTERS[0];
  const trimmedQuery = query.trim().toLowerCase();
  const visibleVms = useMemo(() => {
    return vms
      .filter(activeFilter.predicate)
      .filter(
        vm =>
          trimmedQuery === '' ||
          vm.entry.namespace.toLowerCase().includes(trimmedQuery) ||
          vm.entry.name.toLowerCase().includes(trimmedQuery)
      );
  }, [vms, activeFilter, trimmedQuery]);
  const emptyText = trimmedQuery !== '' ? `No virtual machines match "${query.trim()}".` : activeFilter.emptyText;

  useEffect(() => {
    if (visibleVms.length === 0) {
      setSelectedKey(null);
      return;
    }
    if (!visibleVms.some(vm => vm.key === selectedKey)) {
      setSelectedKey(visibleVms[0].key);
    }
  }, [visibleVms, selectedKey]);

  const selected = visibleVms.find(vm => vm.key === selectedKey) ?? null;
  const suspectedNow = vms.filter(vm => vm.isFromLatestScan && vm.status.suspected).length;

  return (
    <main className="app-shell">
      <section className="dashboard" aria-label="Guest Watchdog dashboard">
        <header className="toolbar">
          <span className="brand">
            <MonitorIcon />
            <h1 className="brand-title">Guest Watchdog</h1>
          </span>
          {data && <ScanSummary lastScan={data.lastScan} suspectedNow={suspectedNow} />}
        </header>

        {error && (
          <div className="error-banner" role="alert">
            Dashboard API unreachable: {error.message}.
            {lastUpdatedAt
              ? ` Showing data from ${timeAgo(lastUpdatedAt)}.`
              : ' No data has loaded yet.'}
          </div>
        )}

        {loading && !data && <div className="loading-state">Loading…</div>}

        {!loading && data && vms.length === 0 && (
          <div className="empty-state">
            No virtual machines observed yet. Waiting for the first scan to complete.
          </div>
        )}

        {vms.length > 0 && (
          <div className="workspace">
            <aside className="vm-list" aria-label="Virtual machines">
              <div className="vm-list-header">
                <div className="list-heading">
                  <h2 className="panel-title">Virtual machines</h2>
                  <span className="count">{visibleVms.length}</span>
                </div>
                <SearchBox value={query} onChange={setQuery} />
                <FilterRow vms={vms} activeKey={filterKey} onChange={setFilterKey} />
              </div>
              <div className="vm-list-scroll">
                {visibleVms.length === 0 && <div className="empty-state">{emptyText}</div>}
                {visibleVms.map(vm => (
                  <button
                    key={vm.key}
                    type="button"
                    className="vm-button"
                    aria-pressed={vm.key === selectedKey}
                    onClick={() => setSelectedKey(vm.key)}
                  >
                    <span className="vm-name">
                      {vm.entry.namespace}/{vm.entry.name}
                    </span>
                    <span className={`vm-state ${vm.status.tone}`}>
                      <span className="state-dot" />
                      {vm.status.label}
                      {!vm.isFromLatestScan && ' (stale)'}
                    </span>
                  </button>
                ))}
              </div>
            </aside>

            <section className="detail">
              <div className="detail-page-header">
                <h2 className="panel-title">Virtual machine details</h2>
              </div>

              <div className="detail-body">
                {selected ? (
                  // key re-mounts (and re-animates) this on every selection change.
                  <div className="detail-page" key={selected.key}>
                    <div className={`detail-banner ${selected.status.tone}`}>
                      <span className="detail-banner-icon">{TONE_ICON[selected.status.tone]}</span>
                      <span className="detail-name">
                        {selected.entry.namespace}/{selected.entry.name}
                      </span>
                    </div>

                    <h3 className="detail-section-title">Details</h3>
                    <dl className="meta-grid">
                      <div className="meta-card">
                        <span className="meta-icon">
                          <NodeIcon />
                        </span>
                        <div>
                          <dt className="meta-label">Node</dt>
                          <dd className="meta-value">{selected.entry.node || '—'}</dd>
                        </div>
                      </div>
                      <div className="meta-card">
                        <span className="meta-icon">
                          <TagIcon />
                        </span>
                        <div>
                          <dt className="meta-label">VM UID</dt>
                          <dd className="meta-value">{selected.entry.uid || '—'}</dd>
                        </div>
                      </div>
                      <div className="meta-card">
                        <span className="meta-icon">
                          <ClockIcon />
                        </span>
                        <div>
                          <dt className="meta-label">Last observed</dt>
                          <dd className="meta-value" title={selected.entry.updatedAt}>
                            {timeAgo(new Date(selected.entry.updatedAt))}
                          </dd>
                        </div>
                      </div>
                      <div className="meta-card">
                        <span className="meta-icon">
                          <ScanIcon />
                        </span>
                        <div>
                          <dt className="meta-label">Scan</dt>
                          <dd className="meta-value">
                            {selected.current.record.scanId}
                            {!selected.isFromLatestScan && ' (not from the latest scan)'}
                          </dd>
                        </div>
                      </div>
                    </dl>

                    <h3 className="detail-section-title">Latest finding</h3>
                    {/* aria-live scoped here, not the whole pane, so the ticking
                        "Last observed" time above doesn't trigger re-announcements. */}
                    <div className={`evidence ${selected.status.tone}`} aria-live="polite">
                      <span className="evidence-icon">{TONE_ICON[selected.status.tone]}</span>
                      <div>
                        <h4>{selected.status.label}</h4>
                        <p>{findingText(selected.current)}</p>
                      </div>
                    </div>
                  </div>
                ) : (
                  <div className="empty-state">{emptyText}</div>
                )}
              </div>
            </section>
          </div>
        )}
      </section>
    </main>
  );
}
