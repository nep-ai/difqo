import React, { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/geist/wght.css'
import '@fontsource/barlow-condensed/latin-600.css'
import { Specialists } from './specialists.jsx'
import './style.css'

const api = import.meta.env.DEV ? '/api' : ''
const base = import.meta.env.BASE_URL
const statuses = {
  queued: 'Queued',
  claimed: 'Running',
  completed: 'Completed',
  blocked: 'Blocked',
  exhausted: 'Attempt limit reached',
}

function Mark() {
  return <img className="mark" src={base + 'favicon.svg'} alt="" />
}

class RequestError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

async function request(path, { timeoutMs = 8000, ...options } = {}) {
  const timeout = AbortSignal.timeout(timeoutMs)
  const response = await fetch(api + path, {
    ...options,
    cache: 'no-store',
    signal: options.signal ? AbortSignal.any([options.signal, timeout]) : timeout,
  })
  if (!response.ok) throw new RequestError(response.status, (await response.text()).trim())
  return response
}

function Evidence({ id, file, label, download = false }) {
  const [open, setOpen] = useState(false)
  const [content, setContent] = useState({ status: 'loading' })
  const [retry, setRetry] = useState(0)
  const endpoint = '/tasks/' + id + '/evidence/' + file

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    request(endpoint, { signal: controller.signal })
      .then(response => response.text())
      .then(text => setContent({ status: 'ready', text }))
      .catch(error => {
        if (!controller.signal.aborted) {
          setContent({ status: error instanceof RequestError && [404, 409].includes(error.status) ? 'unavailable' : 'failed' })
        }
      })
    return () => controller.abort()
  }, [open, endpoint, retry])

  return <details onToggle={event => {
    setOpen(event.currentTarget.open)
    if (event.currentTarget.open) setContent({ status: 'loading' })
  }}>
    <summary>{label}</summary>
    {content.status === 'loading' && <p role="status">Loading evidence…</p>}
    {content.status === 'unavailable' && <p>This file is unavailable or has changed.</p>}
    {content.status === 'failed' && <div className="actions">
      <p>Couldn’t load this file.</p><button onClick={() => { setContent({ status: 'loading' }); setRetry(value => value + 1) }}>Try again</button>
    </div>}
    {content.status === 'ready' && <>
      <pre className="output" tabIndex={0} aria-label={label}>{content.text || 'This file is empty.'}</pre>
      {download && <a className="download" href={api + endpoint} download={'difqo-' + id + '.diff'}>Download patch</a>}
    </>}
  </details>
}

function PatchReview({ goal }) {
  const [open, setOpen] = useState(false)
  const [review, setReview] = useState(null)
  const [busy, setBusy] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [loadFailed, setLoadFailed] = useState(false)
  const [actionError, setActionError] = useState('')
  const { result } = goal.attempt
  const snapshot = result.snapshot
  const endpoint = '/tasks/' + goal.id + '/review'
  const check = review?.check
  const approved = review?.approval
  const running = busy === 'rerun' || check?.status === 'running'

  useEffect(() => {
    if (!open || busy) return
    const controller = new AbortController()
    let timer
    async function load() {
      try {
        const response = await request(endpoint, { signal: controller.signal })
        const data = await response.json()
        if (controller.signal.aborted) return
        setReview(data)
        setLoadFailed(false)
        if (data.check?.status === 'running') timer = setTimeout(load, 2000)
      } catch {
        if (!controller.signal.aborted) {
          setReview(null)
          setLoadFailed(true)
        }
      }
    }
    load()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [open, busy, endpoint, refresh])

  async function act(action) {
    setBusy(action)
    setActionError('')
    try {
      await request(endpoint + '/' + action, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        timeoutMs: action === 'rerun' ? 55000 : 8000,
        body: JSON.stringify({ patch_sha256: snapshot.patch_sha256, check_id: check?.id }),
      })
    } catch (err) {
      setActionError(err instanceof RequestError && err.status === 409
        ? err.message
        : 'Couldn’t confirm the action. Review the current status before retrying.')
    } finally {
      setReview(null)
      setBusy('')
    }
  }

  let status = 'Rerun the check to approve this patch.'
  if (check?.status === 'failed') status = 'Rerun failed (exit ' + check.exit_code + ').'
  if (check?.status === 'interrupted') status = 'Rerun interrupted. Run the check again.'
  if (check?.status === 'passed') status = 'Rerun passed.'
  if (approved) status = 'Patch approved.'
  if (review && !review.patch_available) status = 'The saved patch is missing or has changed.'
  if (running) status = 'Running the check on the saved base and patch…'
  if (!review && !busy) status = ''

  return <details className="review" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>Review patch</summary>
    <dl className="facts review-facts">
      <dt>Project decision</dt><dd>{goal.decision ? <>Revision {goal.decision.id}<p>{goal.decision.body}</p></> : 'No decision was saved for this goal.'}</dd>
      <dt>Base commit</dt><dd><code>{snapshot.base}</code></dd>
      <dt>Patch SHA-256</dt><dd><code>{snapshot.patch_sha256}</code></dd>
      <dt>Check command</dt><dd><code>{snapshot.command}</code></dd>
      <dt>Original check</dt><dd>{result.exit_code === 0 ? 'Passed' : 'Exit ' + result.exit_code}</dd>
    </dl>
    <Evidence id={goal.id} file="change.diff" label="View patch" download />
    <Evidence id={goal.id} file="check.log" label="View original check log" />
    {result.exit_code !== 0 && <Evidence id={goal.id} file="execution.log" label="View execution diagnostic" />}
    {check?.output && !running && <details>
      <summary>View rerun log</summary>
      <pre className="output" tabIndex={0} aria-label="Rerun log">{check.output}</pre>
    </details>}
    {open && !review && !busy && !loadFailed && <p role="status">Loading review…</p>}
    {status && <p role="status" className="review-status">{status}</p>}
    {loadFailed && <p role="alert">Couldn’t load patch review.</p>}
    {actionError && <p role="alert">{actionError}</p>}
    <div className="actions">
      {!review && loadFailed && <button onClick={() => { setLoadFailed(false); setRefresh(value => value + 1) }}>Refresh review</button>}
      {review?.patch_available && !approved && <>
        <button disabled={running || Boolean(busy)} onClick={() => act('rerun')}>Rerun check</button>
        <button className="primary" disabled={running || Boolean(busy) || check?.status !== 'passed'} onClick={() => act('approve')}>Approve patch</button>
      </>}
    </div>
    {review?.patch_available && !approved && <p>Approval records your review. Apply the downloaded patch manually.</p>}
  </details>
}

function ProjectDecision({ dialog }) {
  const [body, setBody] = useState('')
  const [revision, setRevision] = useState(null)
  const [loading, setLoading] = useState(false)
  const [ready, setReady] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const pendingLoad = useRef(null)

  async function load() {
    pendingLoad.current?.abort()
    const controller = new AbortController()
    pendingLoad.current = controller
    dialog.current.showModal()
    setLoading(true)
    setReady(false)
    setError('')
    try {
      const response = await request('/project-decision', { signal: controller.signal })
      const current = response.status === 204 ? null : await response.json()
      if (controller.signal.aborted) return
      setBody(current?.body ?? '')
      setRevision(current?.id ?? null)
      setReady(true)
    } catch {
      if (!controller.signal.aborted) setError('Couldn’t load the saved decision. Reopen this dialog to try again.')
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }

  async function save(event) {
    event.preventDefault()
    if (!body.trim() || saving || !ready) return
    setSaving(true)
    setError('')
    try {
      await request('/project-decision', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ body }),
      })
      dialog.current.close()
    } catch {
      setError('Couldn’t confirm the save. Your draft is still here.')
    } finally {
      setSaving(false)
    }
  }

  return <>
    <button type="button" className="decision-trigger" onClick={load}>Project decision</button>
    <dialog ref={dialog} aria-labelledby="decision-title" onCancel={event => {
      if (saving) event.preventDefault()
      else pendingLoad.current?.abort()
    }}>
      <h2 id="decision-title">Project decision</h2>
      <p>New goals use the latest saved revision.</p>
      <form onSubmit={save}>
        <label htmlFor="decision-body">{revision ? 'Decision · Revision ' + revision : 'Decision'}</label>
        <textarea id="decision-body" rows={5} maxLength={4096} value={body}
          onChange={event => setBody(event.target.value)} readOnly={!ready || saving} />
        {loading && <p role="status">Loading decision…</p>}
        {error && <p role="alert">{error}</p>}
        <div className="actions">
          <button type="button" disabled={saving} onClick={() => {
            pendingLoad.current?.abort()
            dialog.current.close()
          }}>Cancel</button>
          <button className="primary" disabled={!ready || saving || !body.trim()}>{saving ? 'Saving…' : 'Save decision'}</button>
        </div>
      </form>
    </dialog>
  </>
}

function ProjectFile({ goal }) {
  const result = goal.attempt?.result
  const completed = goal.status === 'completed'
  const failed = completed && result?.exit_code !== 0
  const status = failed ? 'Run failed' : statuses[goal.status] ?? goal.status
  return <section className="attachment" aria-label={'Goal ' + goal.id}>
    <div className="project-file-meta"><span>Project file</span><span>Goal {goal.id}</span></div>
    <div className="attachment-title">
      <h2>{goal.goal}</h2>
      <span className={'badge ' + (failed ? 'failure' : goal.status === 'blocked' ? 'attention' : completed ? 'good' : '')} role="status">{status}</span>
    </div>
    {goal.status === 'queued' && <p>The goal is saved. Waiting for a local worker to pick it up.</p>}
    {goal.status === 'claimed' && <p>The local worker is running this goal.</p>}
    {goal.status === 'blocked' && <p>Restore Codex sign-in in the worker’s execution environment, then submit the goal again.</p>}
    {goal.status === 'exhausted' && <p>Check that a local worker is available before submitting this goal again.</p>}
    {result && <dl className="facts">
      <dt>Worker result</dt><dd>{result.output || 'No output was recorded.'}</dd>
      <dt>Exit code</dt><dd><code>{result.exit_code}</code></dd>
    </dl>}
    {result?.snapshot && <PatchReview goal={goal} key={goal.attempt.id} />}
    {result?.artifacts && !result.snapshot && <details className="review" key={goal.attempt.id}>
      <summary>View run evidence</summary>
      <Evidence id={goal.id} file="change.diff" label="View patch" download />
      <Evidence id={goal.id} file="check.log" label="View logs" />
      {failed && <Evidence id={goal.id} file="execution.log" label="View execution diagnostic" />}
    </details>}
  </section>
}

function Chat({ goals, connection, submit }) {
  const [draft, setDraft] = useState('')
  const [showSpecialists, setShowSpecialists] = useState(false)
  const specialistTrigger = useRef(null)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const composer = useRef(null)
  const decisionDialog = useRef(null)
  const end = useRef(null)
  const submitted = useRef(false)
  const openedHistory = useRef(false)
  const latest = goals.at(-1)?.id

  useEffect(() => {
    if (latest && (!openedHistory.current || submitted.current)) {
      openedHistory.current = true
      end.current?.scrollIntoView({ block: 'nearest' })
      if (submitted.current) composer.current?.focus({ preventScroll: true })
      submitted.current = false
    }
  }, [latest])

  async function send(event) {
    event.preventDefault()
    if (!draft.trim() || sending || connection !== 'ready') return
    const goal = draft.trim()
    setSending(true)
    setError('')
    try {
      submitted.current = true
      await submit(goal)
      setDraft('')
    } catch (err) {
      submitted.current = false
      setError(err instanceof RequestError && err.status === 400
        ? 'Enter a goal of up to 4,096 characters.'
        : 'Couldn’t confirm submission. Check the conversation before retrying. Your draft is still here.')
    } finally {
      setSending(false)
    }
  }

  return <main className="chat" aria-labelledby="conversation-title">
    <h1 id="conversation-title" className="sr-only">Chief of Staff conversation</h1>
    {goals.length ? <section className="conversation" aria-label="Conversation">
      {goals.map(goal => <React.Fragment key={goal.id}>
        <article className="turn user" aria-label="You"><div className="turn-content"><p>{goal.goal}</p></div></article>
        <article className="turn chief" aria-label="Chief of Staff"><Mark /><div className="turn-content">
          <strong className="speaker">Chief of Staff</strong><ProjectFile goal={goal} />
        </div></article>
      </React.Fragment>)}
      <div ref={end} />
    </section> : <section className="welcome">
      <Mark />
      <h2>What would you like to get done?</h2>
      <p>Give the Chief of Staff a goal. You’ll see what the local worker did and what evidence is available here.</p>
    </section>}
    {showSpecialists && <Specialists request={request} mark={<Mark />} close={() => {
      setShowSpecialists(false)
      specialistTrigger.current?.focus()
    }} />}
    <div className="composer">
      <form onSubmit={send}>
        {error && <p className="submit-error" role="alert">{error}</p>}
        <div className="compose-box">
          <label htmlFor="message" className="sr-only">Message your Chief of Staff</label>
          <textarea id="message" ref={composer} rows={2} maxLength={4096} value={draft}
            onChange={event => setDraft(event.target.value)} readOnly={sending} placeholder="Describe your goal"
            onKeyDown={event => {
              if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault()
                event.currentTarget.form.requestSubmit()
              }
            }} />
          <button className="primary" disabled={!draft.trim() || sending || connection !== 'ready'}>{sending ? 'Sending…' : 'Send'}</button>
        </div>
      </form>
      <div className="composer-meta"><div className="specialist-actions">
        <ProjectDecision dialog={decisionDialog} />
        <button className="subtle" type="button" ref={specialistTrigger} aria-expanded={showSpecialists}
          onClick={() => setShowSpecialists(open => !open)}>Specialists</button>
      </div><span>Shift + Enter for a new line</span></div>
    </div>
  </main>
}

function App() {
  const [connection, setConnection] = useState('checking')
  const [goals, setGoals] = useState([])
  const dialog = useRef(null)
  const generation = useRef(0)

  useEffect(() => {
    const controller = new AbortController()
    let timer
    async function refresh() {
      const current = generation.current
      try {
        const response = await request('/tasks', { signal: controller.signal })
        const recent = await response.json()
        if (!Array.isArray(recent)) throw new Error('Invalid task history')
        if (!controller.signal.aborted && current === generation.current) {
          setGoals(recent.toReversed())
          setConnection('ready')
        }
      } catch {
        if (!controller.signal.aborted && current === generation.current) setConnection('offline')
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(refresh, 3000)
      }
    }
    refresh()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [])

  async function submit(goal) {
    const response = await request('/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ goal }),
    })
    const saved = await response.json()
    generation.current++
    setGoals(current => current.some(item => item.id === saved.id) ? current : [...current, saved].slice(-100))
  }

  return <div className="app">
    <header className="topbar"><div className="brand"><Mark /><span>difqo<span className="wordmark-period">.</span></span></div>
      <span className="surface-title">Chief of Staff</span>
      <div className="top-actions">
        <span className={'badge ' + (connection === 'ready' ? 'good' : connection === 'offline' ? 'attention' : '')} role="status">
          {{ ready: 'Connected', checking: 'Connecting', offline: 'Service unavailable' }[connection]}
        </span>
        <button className="subtle" onClick={() => dialog.current.showModal()}>Local service</button>
      </div>
    </header>
    {connection === 'offline' && <p className="connection-note" role="status">Couldn’t reach the local service. Check that Difqo and PostgreSQL are running. This page reconnects automatically.</p>}
    <Chat goals={goals} connection={connection} submit={submit} />
    <dialog ref={dialog} aria-labelledby="service-title">
      <h2 id="service-title">Local service</h2>
      <p>Goals are stored by Difqo in PostgreSQL. A local worker claims and runs them. Start another worker invocation when you want it to take another queued goal.</p>
      <form method="dialog"><button className="primary">Done</button></form>
    </dialog>
  </div>
}

createRoot(document.getElementById('root')).render(<App />)
