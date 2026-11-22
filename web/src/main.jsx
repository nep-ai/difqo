import React, { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import {
  answer, baseRevision, checkCommand, freshDemo, loadDemo, patch, patchRevision,
  readiness, sampleGoal, saveDemo,
} from './demo.js'
import '@fontsource-variable/geist/wght.css'
import '@fontsource/barlow-condensed/latin-600.css'
import favicon from '/favicon.svg?url'
import './style.css'

function Mark() {
  return <img className="mark" src={favicon} alt="" />
}

function Installation({ onInstall }) {
  return <main className="setup" aria-labelledby="install-title">
    <h1 id="install-title">Install Difqo locally</h1>
    <p>The CLI configures the control plane, PostgreSQL, and isolated execution. Difqo then runs as a background service.</p>
    <section className="terminal" aria-label="Terminal preview">
      <div className="terminal-head">Terminal</div>
      <pre><span>$ difqo install</span>{'\nConfigure local services and open the browser.'}</pre>
    </section>
    <button className="primary" onClick={onInstall}>Simulate installation</button>
    <details>
      <summary>Manage the local service</summary>
      <dl className="facts">
        <dt>Inspect the service and find the local URL</dt><dd><code>difqo status</code></dd>
        <dt>Start or resume dispatch</dt><dd><code>difqo start</code></dd>
        <dt>Pause new work</dt><dd><code>difqo pause</code></dd>
        <dt>Update</dt><dd><code>difqo update</code></dd>
        <dt>Uninstall</dt><dd><code>difqo uninstall</code></dd>
      </dl>
      <p className="meta">Commands shown here are proposed.</p>
    </details>
  </main>
}

function Readiness({ blocker, update, onContinue }) {
  return <main className="setup" aria-labelledby="readiness-title">
    <h1 id="readiness-title">Check your workspace</h1>
    <p>Difqo verifies what the Chief of Staff needs before it can start work.</p>
    <section className="surface checks" aria-label="Readiness checks">
      <p className="check-summary" role="status">{blocker ? 'One check needs attention' : 'All checks passed'}</p>
      {readiness.map(({ id, name, description, problem, action }) => <div className="check-row" key={id}>
        <div><strong>{name}</strong><p>{blocker === id ? problem : description}</p>
          {blocker === id && <button onClick={() => update('')}>{action}</button>}
        </div>
        <span className={'badge ' + (blocker === id ? 'attention' : 'good')}>{blocker === id ? 'Blocked' : 'Ready'}</span>
      </div>)}
    </section>
    <button className="primary" disabled={Boolean(blocker)} onClick={onContinue}>Meet your Chief of Staff</button>
    <details>
      <summary>Try a readiness problem</summary>
      <div className="actions">{readiness.map(({ id, name }) => <button key={id} onClick={() => update(id)}>{name}</button>)}</div>
    </details>
  </main>
}

function downloadPatch() {
  const url = URL.createObjectURL(new Blob([patch], { type: 'text/plain' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'workspace-isolation-' + patchRevision + '.diff'
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function GoalCard({ goal, run, stop, verify, accept }) {
  const { id, title, status, sample, check } = goal
  const reviewing = sample && ['Ready for review', 'Accepted', 'Changes requested'].includes(status)
  return <section className="attachment" aria-label={'Work on ' + title}>
    <div className="project-file-meta"><span>Project file</span><span>{sample ? 'orchard/reports · sample repository' : 'Sample workspace'}</span></div>
    <div className="attachment-title"><h2>{title}</h2><span className={'badge ' + (status === 'Accepted' ? 'good' : '')}>{status}</span></div>
    {status === 'Proposed' && <>
      <p>{sample ? 'Verify that one workspace cannot export another workspace’s rows in the sample repository.' : 'For a real run, I’d confirm the repository, inspect its saved decisions, and propose how to verify the outcome.'}</p>
      <details><summary>Scope and permissions</summary><p>Read repository files and make changes in an isolated checkout. Ask before posting externally.</p></details>
      <button className="primary" onClick={() => run(id)}>Start run</button>
    </>}
    {status === 'Running' && <>
      <p role="status">The goal is saved. The Chief of Staff is coordinating the work and checking the result.</p>
      <div className="actions">
        <button onClick={() => stop(id, 'Paused')}>Pause run</button><button onClick={() => stop(id, 'Cancelled')}>Cancel run</button>
      </div>
    </>}
    {status === 'Paused' && <div className="actions"><button className="primary" onClick={() => run(id)}>Resume run</button><button onClick={() => stop(id, 'Cancelled')}>Cancel run</button></div>}
    {status === 'Completed' && <p>Simulation complete. A real run would show evidence specific to this goal.</p>}
    {reviewing && <>
      <p>The patch limits report exports to the requesting workspace. The isolation check uses records from two workspaces.</p>
      <details className="review"><summary>Review changes</summary>
        <dl className="facts">
          <dt>Saved decision</dt><dd>Workspace isolation. Exports include only rows owned by the active workspace.</dd>
          <dt>Original base</dt><dd><code>{baseRevision}</code></dd>
          <dt>Patch</dt><dd><code>{patchRevision}</code></dd>
          <dt>Check</dt><dd><span role="status" className={'badge ' + (check === 'Passed' ? 'good' : 'attention')}>{check === 'Required' ? 'Check out of date' : check}</span></dd>
        </dl>
        <details><summary>View patch</summary><pre className="output" tabIndex={0} aria-label="Patch diff">{patch.trimEnd().split('\n').map((line, index) => <span key={index} className={line.startsWith('+') ? 'addition' : line.startsWith('-') ? 'deletion' : ''}>{line}{'\n'}</span>)}</pre></details>
        <details><summary>View logs</summary><pre className="output" tabIndex={0} aria-label="Check logs">{'$ ' + checkCommand + '\n' + (check === 'Passed' ? 'PASS · TestExportWorkspaceIsolation' : check === 'Checking' ? 'Reconstructing the original base and patch…' : 'Rerun the check on this patch before approving.')}</pre></details>
        {status === 'Ready for review' && <div className="actions">
          <button className={check === 'Required' ? 'primary' : ''} onClick={() => verify(id)} disabled={check === 'Checking'}>Rerun check</button>
          <button className={check === 'Passed' ? 'primary' : ''} onClick={() => accept(id, 'Accepted')} disabled={check !== 'Passed'}>Approve patch</button>
          <button onClick={() => accept(id, 'Changes requested')}>Request changes</button>
        </div>}
        {status === 'Accepted' && <p>Approval recorded for patch <code>{patchRevision}</code> and its passing check.</p>}
        <button className="subtle download" onClick={downloadPatch}>Download patch</button>
      </details>
    </>}
  </section>
}

function ApprovalCard({ approval, decide }) {
  const { request, scope, status } = approval
  const label = { github: 'GitHub', slack: 'Slack', 'mcp server': 'MCP server' }[request] ?? request
  return <section className="attachment" aria-label={'Approval for ' + label}>
    <div className="attachment-title"><h2>Approve access for {label}?</h2><span className="badge">{status}</span></div>
    <p>{scope}</p>
    {status === 'Pending' && <div className="actions">
      <button className="primary" onClick={() => decide('Approved')}>Approve access</button><button onClick={() => decide('Declined')}>Decline</button>
    </div>}
  </section>
}

function Chat({ demo, send, run, stop, verify, accept, decide }) {
  const [draft, setDraft] = useState('')
  const composer = useRef(null)
  const end = useRef(null)
  const count = demo.messages.length
  const attachments = new Map()
  for (const [index, message] of demo.messages.entries()) {
    if (message.goal && (message.moveCard || !attachments.has(message.goal))) {
      attachments.set(message.goal, index)
    }
  }

  const follow = useRef(true)

  useEffect(() => {
    function trackReadingPosition() {
      const remaining = document.documentElement.scrollHeight - window.innerHeight - window.scrollY
      follow.current = remaining < 144
    }
    window.addEventListener('scroll', trackReadingPosition, { passive: true })
    return () => window.removeEventListener('scroll', trackReadingPosition)
  }, [])

  useEffect(() => {
    if (count && follow.current) end.current?.scrollIntoView({ block: 'nearest' })
  }, [count])

  function submit(text) {
    const trimmed = text.trim()
    if (!trimmed) return
    follow.current = true
    send(trimmed)
    setDraft('')
    composer.current.focus({ preventScroll: true })
  }

  return <main className="chat" aria-labelledby="conversation-title">
    <h1 id="conversation-title" className="sr-only">Chief of Staff conversation</h1>
    <p className="sr-only" role="status">{demo.messages.at(-1)?.role === 'chief' ? demo.messages.at(-1).text : ''}</p>
    {count ? <section className="conversation" aria-label="Conversation">
      {demo.messages.map((message, index) => {
        const hasWorkCard = message.goal && attachments.get(message.goal) === index
        return <article className={'turn ' + message.role} aria-label={message.role === 'chief' ? 'Chief of Staff' : 'You'} key={index}>
          {message.role === 'chief' && <Mark />}
          <div className="turn-content">
            {message.role === 'chief' && <strong className="speaker">Chief of Staff</strong>}
            <p>{message.text}</p>
            {hasWorkCard && <div id={'goal-' + message.goal}><GoalCard goal={demo.goals[message.goal]} {...{ run, stop, verify, accept }} /></div>}
            {message.goal && !hasWorkCard && <a className="conversation-link" href={'#goal-' + message.goal}>Open project file</a>}
            {message.approval && <ApprovalCard approval={demo.approvals[message.approval]} decide={status => decide(message.approval, status)} />}
          </div>
        </article>
      })}
      <div ref={end} />
    </section> : <section className="welcome">
      <Mark />
      <h2>What would you like to get done?</h2>
      <p>I’m your Chief of Staff. Tell me what you want to accomplish. I’ll coordinate the work and bring you the result.</p>
      <div className="starter">
        <button onClick={() => submit(sampleGoal)}><strong>Try a sample goal</strong><span>{sampleGoal}</span></button>
        <button onClick={() => submit('What’s the status of my work?')}><strong>Ask for an update</strong><span>What’s the status of my work?</span></button>
      </div>
    </section>}
    <form className="composer" onSubmit={event => { event.preventDefault(); submit(draft) }}>
      <div className="compose-box">
        <label htmlFor="message" className="sr-only">Message your Chief of Staff</label>
        <textarea id="message" ref={composer} rows={2} maxLength={800} value={draft}
          onChange={event => setDraft(event.target.value)} placeholder="Message your Chief of Staff"
          onKeyDown={event => {
            if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault()
              submit(draft)
            }
          }} />
        <button className="primary" disabled={!draft.trim()}>Send</button>
      </div>
      <div className="composer-meta"><span>Local workspace</span><span>Shift + Enter for a new line</span></div>
    </form>
  </main>
}

function App() {
  const [demo, setDemo] = useState(loadDemo)
  const dialog = useRef(null)
  const timers = useRef(new Map())
  useEffect(() => saveDemo(demo), [demo])
  useEffect(() => () => timers.current.forEach(clearTimeout), [])

  function changeGoal(id, change, message) {
    setDemo(current => {
      const goal = current.goals[id]
      const next = goal && change(goal)
      if (!next) return current
      const reply = typeof message === 'function' ? message(next) : message
      return {
        ...current,
        goals: { ...current.goals, [id]: next },
        messages: reply ? [...current.messages, { role: 'chief', text: reply, goal: id, moveCard: next.status !== goal.status && !['Accepted', 'Changes requested'].includes(next.status) }] : current.messages,
      }
    })
  }

  function later(key, delay, action) {
    clearTimeout(timers.current.get(key))
    timers.current.set(key, setTimeout(() => {
      timers.current.delete(key)
      action()
    }, delay))
  }

  function run(id) {
    changeGoal(id, goal => ['Proposed', 'Paused'].includes(goal.status) ? { ...goal, status: 'Running' } : null)
    later(id, 1000, () => changeGoal(id, goal => goal.status === 'Running'
      ? { ...goal, status: goal.sample ? 'Ready for review' : 'Completed', check: goal.sample ? 'Passed' : 'Required' }
      : null, goal => goal.sample
      ? 'The sample check passed. I’ve attached the patch and the evidence so you can review the result.'
      : 'The simulation is complete. I’ve attached the outcome here.'))
  }

  function stop(id, status) {
    clearTimeout(timers.current.get(id))
    timers.current.delete(id)
    changeGoal(id, goal => ['Running', 'Paused'].includes(goal.status) ? { ...goal, status } : null,
      status === 'Paused' ? 'I’ve paused the goal. You can resume it here.' : 'I’ve cancelled the goal and kept its record in this conversation.')
  }

  function verify(id) {
    changeGoal(id, goal => goal.status === 'Ready for review' ? { ...goal, check: 'Checking' } : null)
    later('check-' + id, 700, () => changeGoal(id,
      goal => goal.status === 'Ready for review' && goal.check === 'Checking' ? { ...goal, check: 'Passed' } : null,
      'The check passed again on the original base and exact patch.'))
  }

  function accept(id, status) {
    changeGoal(id, goal => goal.status === 'Ready for review' && (status !== 'Accepted' || goal.check === 'Passed')
      ? { ...goal, status }
      : null, status === 'Accepted' ? 'I recorded your approval for this patch and its passing check.' : 'I recorded that changes are needed. Tell me what you’d like adjusted.')
  }

  function decide(id, status) {
    setDemo(current => {
      const approval = current.approvals[id]
      if (approval.status !== 'Pending') return current
      return {
        ...current,
        approvals: { ...current.approvals, [id]: { ...approval, status } },
        messages: [...current.messages, { role: 'chief',
          text: status === 'Approved' ? 'I recorded the approval for the displayed scope in this demo.' : 'I recorded your decision and left access unchanged.' }],
      }
    })
  }

  function reset() {
    timers.current.forEach(clearTimeout)
    timers.current.clear()
    dialog.current.close()
    setDemo(freshDemo())
  }

  return <div className="app">
    <header className="topbar"><div className="brand"><Mark /><span>difqo<span className="wordmark-period">.</span></span></div>
      <span className="surface-title">{demo.stage === 'chat' ? 'Chief of Staff' : 'Local setup'}</span>
      <div className="top-actions"><span className="badge">Workflow demo</span><button className="subtle" onClick={() => dialog.current.showModal()}>Local service</button></div>
    </header>
    {demo.stage === 'install' && <Installation onInstall={() => setDemo(current => ({ ...current, stage: 'readiness' }))} />}
    {demo.stage === 'readiness' && <Readiness blocker={demo.blocker}
      update={blocker => setDemo(current => ({ ...current, blocker }))}
      onContinue={() => setDemo(current => ({ ...current, stage: 'chat' }))} />}
    {demo.stage === 'chat' && <Chat demo={demo} send={text => setDemo(current => answer(current, text))} {...{ run, stop, verify, accept, decide }} />}
    <dialog ref={dialog} aria-labelledby="service-title">
      <h2 id="service-title">Local service</h2>
      <p>This frontend uses sample data stored in this browser. Installation, readiness, and work are simulated.</p>
      <div className="actions"><button onClick={reset}>Reset this demo</button><form method="dialog"><button className="primary">Done</button></form></div>
    </dialog>
  </div>
}

createRoot(document.getElementById('root')).render(<App />)
