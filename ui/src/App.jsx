import { useState } from 'react';

const virtualMachines = [
  {
    id: 'windows', name: 'win11-build-042', status: 'Suspected stall', tone: 'amber',
    caption: 'Windows Setup', kind: 'progress', heading: 'Installing Windows',
    subheading: 'Getting files ready for installation', progress: 42,
    progressLeft: '42% complete', progressRight: 'Unchanged for 14m',
    finding: 'Progress has stopped changing',
    explanation: 'Installation stayed at 42% across repeated captures for 14 minutes, exceeding this example’s 10-minute limit. Review the guest to confirm a stall.'
  },
  {
    id: 'panic', name: 'rhel-worker-03', status: 'Suspected crash', tone: 'red',
    caption: 'Linux console', kind: 'terminal',
    terminal: '[   36.219] Kernel panic - not syncing:\n[   36.219] VFS: Unable to mount root fs\n[   36.220] Call Trace:\n[   36.220]  <TASK> panic+0x10f/0x2e0',
    finding: 'Kernel panic text is visible',
    explanation: 'The latest capture contains a kernel panic message. The model flags a suspected guest crash and keeps the visible evidence for review.'
  },
  {
    id: 'progress', name: 'rhel-build-018', status: 'Progressing', tone: 'blue',
    caption: 'Linux installation', kind: 'progress', heading: 'Installing packages',
    subheading: '1,178 / 1,732 packages installed', progress: 68,
    progressLeft: '68% complete', progressRight: 'Last capture: 65%',
    finding: 'The installation is advancing',
    explanation: 'The package count advanced between the last two captures. Meaningful progress is visible, so the installation timeout has not been reached.'
  },
  {
    id: 'login', name: 'fedora-dev-01', status: 'Expected screen reached', tone: 'green',
    caption: 'Linux console', kind: 'terminal',
    terminal: 'Fedora Linux\n\nAll services started.\n\nfedora-dev-01 login: _',
    finding: 'The expected login prompt appeared',
    explanation: 'This boot-to-login goal is complete. The login prompt matches the expected screen; application responsiveness requires additional checks.'
  }
];

function MonitorIcon() {
  return <img className="brand-icon" src="/icon.svg" alt="" aria-hidden="true"/>;
}

export default function App() {
  const [selectedId, setSelectedId] = useState(virtualMachines[0].id);
  const selected = virtualMachines.find(vm => vm.id === selectedId);

  return (
    <main className="app-shell">
      <section className="dashboard" aria-label="Guest Watchdog dashboard">
        <header className="toolbar">
          <span className="brand"><MonitorIcon />Guest Watchdog</span>
        </header>

        <div className="workspace">
          <aside className="vm-list" aria-label="Virtual machines">
            <div className="list-heading"><span>Virtual machines</span><span className="count">{virtualMachines.length}</span></div>
            {virtualMachines.map(vm => (
              <button key={vm.id} type="button" className="vm-button" aria-pressed={vm.id === selectedId} onClick={() => setSelectedId(vm.id)}>
                <span className="vm-name">{vm.name}</span>
                <span className={`vm-state ${vm.tone}`}><span className="state-dot" />{vm.status}</span>
              </button>
            ))}
          </aside>

          <section className="detail" aria-live="polite">
            <div className="detail-heading">
              <span className="detail-name">{selected.name}</span>
              <span className={`status-badge ${selected.tone}`}>{selected.status}</span>
            </div>

            <div className={`console ${selected.kind === 'terminal' ? 'terminal-console' : ''}`}>
              <div className="console-toolbar"><span>{selected.caption}</span><span>Illustrative console</span></div>
              <div className="console-content">
                {selected.kind === 'terminal' ? (
                  <pre>{selected.terminal}</pre>
                ) : (
                  <>
                    <div className="console-title">{selected.heading}</div>
                    <div className="console-description">{selected.subheading}</div>
                    <div className="progress-track" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={selected.progress}>
                      <span style={{ width: `${selected.progress}%` }} />
                    </div>
                    <div className="console-foot"><span>{selected.progressLeft}</span><span>{selected.progressRight}</span></div>
                  </>
                )}
              </div>
            </div>

            <div className="evidence">
              <h1>{selected.finding}</h1>
              <p>{selected.explanation}</p>
            </div>
          </section>
        </div>
      </section>
    </main>
  );
}
