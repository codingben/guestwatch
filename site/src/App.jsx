import { useEffect, useState } from 'react';

const vms = [
  {id:'windows',name:'win11-build-042',status:'Suspected stall',tone:'amber',caption:'Windows Setup',kind:'progress',heading:'Installing Windows',subheading:'Getting files ready for installation',progress:42,left:'42% complete',right:'Unchanged for 14m',finding:'Progress has stopped changing',explanation:'Installation stayed at 42% across repeated captures for 14 minutes, exceeding this example’s 10-minute limit. Review the guest to confirm a stall.',report:{tone:'amber',cause:'Likely stalled',confidence:'Medium confidence',summary:'Progress has not changed in 14 minutes, past the configured timeout — this looks stalled rather than just slow.'}},
  {id:'panic',name:'rhel-worker-03',status:'Suspected crash',tone:'red',caption:'Linux console',kind:'terminal',terminal:'[   36.219] Kernel panic - not syncing:\n[   36.219] VFS: Unable to mount root fs\n[   36.220] Call Trace:\n[   36.220]  <TASK> panic+0x10f/0x2e0',finding:'Kernel panic text is visible',explanation:'The latest capture contains a kernel panic message. The model flags a suspected guest crash and keeps the visible evidence for review.',report:{tone:'red',cause:'Kernel panic',confidence:'High confidence',summary:'The console still shows an unrecovered kernel panic with no further boot progress — the guest needs a restart.'}},
  {id:'progress',name:'rhel-build-018',status:'Progressing',tone:'blue',caption:'Linux installation',kind:'progress',heading:'Installing packages',subheading:'1,178 / 1,732 packages installed',progress:68,left:'68% complete',right:'Last capture: 65%',finding:'The installation is advancing',explanation:'The package count advanced between the last two captures. Meaningful progress is visible, so the installation timeout has not been reached.',report:{tone:'blue',cause:'On track',confidence:'High confidence',summary:'Package installation is advancing normally between captures — no stall or error detected.'}},
  {id:'login',name:'fedora-dev-01',status:'Expected screen reached',tone:'green',caption:'Linux console',kind:'terminal',terminal:'Fedora Linux\n\nAll services started.\n\nfedora-dev-01 login: _',finding:'The expected login prompt appeared',explanation:'This boot-to-login goal is complete. The login prompt matches the expected screen; application responsiveness requires additional checks.',report:{tone:'green',cause:'No issue found',confidence:'High confidence',summary:'The console shows the expected login prompt with no error text — nothing further to investigate.'}}
];

const installSteps = [
  {title:'Prepare your model credentials',note:<>Save your API key in <code>api-key.txt</code>.</>,code:'kubectl -n images create secret generic guestwatch-model --from-file=api-key=./api-key.txt'},
  {title:'Render and apply the deployment',note:<>Set <code>NAMESPACE</code> and <code>MODEL_ID</code>, then render the template before applying it.</>,code:'NAMESPACE=images MODEL_ID=your-model envsubst < deploy/guestwatch.yaml | kubectl apply -f -'},
  {title:'Open the local dashboard',code:'kubectl -n images port-forward svc/guestwatch 8080:80',after:<>After deployment, visit <code>http://localhost:8080</code>. Viewing stored observations does not trigger an AI request.</>}
];

const faqs = [
  ['Will I need cluster-admin access?','The deployment targets namespace administrators: a Deployment, Service, ConfigMap, and namespaced RBAC, with no new CRDs or cluster-wide roles. Your cluster must already provide KubeVirt, and you must be allowed to grant the required VM and console access.'],
  ['Does the dashboard need a chatbot prompt?','The dashboard shows recorded observations automatically. Periodic checks perform model analysis; viewing results does not need a new prompt or model call. Console images go to your configured model provider, and its charges apply to background checks.'],
  ['Can a still screen prove an app is frozen?','A still screen can be normal. Progress monitoring needs repeated readable captures, an expected workflow, and a timeout. The AI agent can surface suspected stalls in visible workflows; application probes or guest telemetry are needed for problems that do not appear on the console.'],
  ['What can I use today?',<>The <a href="https://github.com/codingben/guestwatch">agent</a> contains periodic screenshot classification and structured logging. The <a href="https://github.com/codingben/kubevirt-console-mcp">console MCP server</a> provides read-only console evidence.</>]
];

function MonitorIcon({small=false}) { return <img className={small?'small-icon':'brand-icon'} src="/icon.svg" alt="" aria-hidden="true"/> }

function GithubIcon() { return <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path fill="currentColor" d="M12 .75a11.25 11.25 0 0 0-3.56 21.92c.56.1.77-.24.77-.54v-2.1c-3.13.68-3.79-1.33-3.79-1.33-.51-1.3-1.25-1.64-1.25-1.64-1.02-.7.08-.69.08-.69 1.13.08 1.72 1.16 1.72 1.16 1 1.72 2.63 1.22 3.27.93.1-.73.4-1.22.72-1.5-2.5-.29-5.13-1.25-5.13-5.57 0-1.23.44-2.24 1.16-3.03-.12-.28-.5-1.43.11-2.98 0 0 .95-.31 3.1 1.15a10.83 10.83 0 0 1 5.64 0c2.15-1.46 3.1-1.15 3.1-1.15.61 1.55.23 2.7.11 2.98.72.79 1.16 1.8 1.16 3.03 0 4.33-2.64 5.28-5.15 5.56.4.35.76 1.04.76 2.1v3.08c0 .3.2.65.78.54A11.25 11.25 0 0 0 12 .75Z"/></svg> }

function SunIcon() { return <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/></svg> }

function MoonIcon() { return <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79Z"/></svg> }

function useTheme() {
  const [theme, setTheme] = useState(() => document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light');
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('theme', theme);
  }, [theme]);
  return [theme, setTheme];
}

function ThemeToggle({ theme, onToggle }) {
  return <button className="theme-toggle" onClick={onToggle} aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'} title={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}>
    {theme === 'dark' ? <SunIcon/> : <MoonIcon/>}
  </button>;
}

export default function App() {
  const [theme,setTheme] = useTheme();
  const [selectedId,setSelectedId] = useState(vms[0].id);
  const [copied,setCopied] = useState(null);
  const [reportState,setReportState] = useState({});
  const selected = vms.find(vm=>vm.id===selectedId);
  const investigateState = reportState[selected.id] ?? 'idle';
  async function copy(code,index) {
    try { await navigator.clipboard.writeText(code); setCopied(index); setTimeout(()=>setCopied(null),1600); }
    catch { window.prompt('Copy this command:',code); }
  }
  function investigate(id) {
    if (reportState[id] === 'loading') return;
    setReportState(s=>({...s,[id]:'loading'}));
    setTimeout(()=>setReportState(s=>({...s,[id]:'done'})),1200);
  }
  return <>
    <a className="skip" href="#main">Skip to content</a>
    <header className="site-header"><div className="container nav-wrap">
      <a className="site-brand" href="#"><MonitorIcon/>GuestWatch</a>
      <nav aria-label="Main navigation"><a href="#preview">Preview</a><a href="#how">How it works</a><a href="#install">Install</a><a className="github" href="https://github.com/codingben/guestwatch"><GithubIcon/><span>GitHub</span></a><ThemeToggle theme={theme} onToggle={()=>setTheme(t=>t==='dark'?'light':'dark')}/></nav>
    </div></header>
    <main id="main">
      <section className="hero container"><h1>AI-powered guest monitoring<br/>for KubeVirt VMs.</h1><p>Deploy an AI agent that checks Linux and Windows console screens for signs of trouble. Spot visible crashes, follow installation progress, and monitor expected screens.</p><div className="actions"><a className="primary" href="#preview">Explore the preview →</a><a className="secondary" href="#install">YAML installation</a></div></section>
      <section className="preview container" id="preview" aria-label="Interactive product preview"><div className="preview-window">
        <div className="app-toolbar"><span className="app-title"><MonitorIcon small/>GuestWatch AI</span></div>
        <div className="workspace"><aside className="vm-list"><div className="list-heading"><span>Virtual machines</span><span className="count">4</span></div>{vms.map(vm=><button key={vm.id} className="vm-button" aria-pressed={vm.id===selectedId} onClick={()=>setSelectedId(vm.id)}><span className="vm-name">{vm.name}</span><span className={`vm-state ${vm.tone}`}><i/>{vm.status}</span></button>)}</aside>
          <section className="vm-detail" aria-live="polite"><div className="detail-heading"><span className="detail-name">{selected.name}</span><span className={`badge ${selected.tone}`}>{selected.status}</span></div><div className={`console ${selected.kind==='terminal'?'terminal-console':''}`}><div className="console-toolbar"><span>{selected.caption}</span><span>Illustrative console</span></div><div className="console-content">{selected.kind==='terminal'?<pre>{selected.terminal}</pre>:<><div className="console-title">{selected.heading}</div><div className="console-description">{selected.subheading}</div><div className="progress-track"><span style={{width:`${selected.progress}%`}}/></div><div className="console-foot"><span>{selected.left}</span><span>{selected.right}</span></div></>}</div></div><div className="evidence"><strong>{selected.finding}</strong><p>{selected.explanation}</p></div><button className="investigate-btn" disabled={investigateState==='loading'} onClick={()=>investigate(selected.id)}>{investigateState==='loading'?'Investigating…':investigateState==='done'?'Investigate again':'Investigate'}</button>{investigateState==='done'&&<div className={`report ${selected.report.tone}`}><div className="report-head">Investigation report<span className={`badge ${selected.report.tone}`}>{selected.report.cause}</span><span className="report-confidence">{selected.report.confidence}</span></div><p>{selected.report.summary}</p></div>}</section>
        </div></div></section>
      <section className="section" id="how"><div className="container"><div className="section-intro"><span>Console evidence, at a glance</span><h2>Spend less time watching consoles.</h2><p>From a Linux panic to a Windows installation that stops advancing, the AI agent is built around the screens you already check by hand.</p></div><div className="features"><article><b>01</b><h3>Spot visible crashes</h3><p>Periodically capture the guest console and use a vision model to flag suspected Linux kernel panics or Windows blue screens.</p></article><article><b>02</b><h3>Follow an installation</h3><p>Track meaningful progress across captures. Surface a suspected stall when a stage exceeds your timeout.</p></article><article><b>03</b><h3>Set an expected screen</h3><p>Expect a login prompt after boot, or an application screen after login. Review unexpected prompts and slow steps.</p></article></div></div></section>
      <section className="section install" id="install"><div className="container narrow"><h2>Installation</h2>{installSteps.map((step,index)=><article className="install-step" key={step.title}><h3><span>{index+1}</span>{step.title}</h3>{step.note&&<p>{step.note}</p>}<div className="code-card"><div><span>Shell</span><button onClick={()=>copy(step.code,index)}>{copied===index?'Copied':'Copy'}</button></div><pre><code>{step.code}</code></pre></div>{step.after&&<p>{step.after}</p>}</article>)}</div></section>
      <section className="section"><div className="container narrow"><h2 className="faq-title">FAQ</h2>{faqs.map(([q,a],index)=><details key={q} open={index===0}><summary>{q}</summary><p>{a}</p></details>)}</div></section>
    </main>
    <footer>guestwatch.dev</footer>
  </>;
}
