import React, { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/geist/wght.css'
import '@fontsource/barlow-condensed/latin-600.css'
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
  constructor(status) {
    super('Difqo request failed')
    this.status = status
  }
}

async function request(path, options = {}) {
  const timeout = AbortSignal.timeout(8000)
  const response = await fetch(api + path, {
    ...options,
    cache: 'no-store',
    signal: options.signal ? AbortSignal.any([options.signal, timeout]) : timeout,
  })
  if (!response.ok) throw new RequestError(response.status)
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
          setContent({ status: error instanceof RequestError && error.status === 404 ? 'unavailable' : 'failed' })
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
    {content.status === 'unavailable' && <p>This file is unavailable. Check that the server can read the worker’s artifact directory.</p>}
    {content.status === 'failed' && <div className="actions">
      <p>Couldn’t load this file.</p><button onClick={() => { setContent({ status: 'loading' }); setRetry(value => value + 1) }}>Try again</button>
    </div>}
    {content.status === 'ready' && <>
      <pre className="output" tabIndex={0} aria-label={label}>{content.text || 'This file is empty.'}</pre>
      {download && <a className="download" href={api + endpoint} download={'difqo-' + id + '.diff'}>Download patch</a>}
    </>}
  </details>
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
    {result?.artifacts && <details className="review" key={goal.attempt.id}>
      <summary>View run evidence</summary>
      <Evidence id={goal.id} file="change.diff" label="View patch" download />
      <Evidence id={goal.id} file="check.log" label="View logs" />
      {failed && <Evidence id={goal.id} file="execution.log" label="View execution diagnostic" />}
    </details>}
  </section>
}

function Chat({ goals, connection, submit }) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const composer = useRef(null)
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
    <form className="composer" onSubmit={send}>
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
      <div className="composer-meta"><span>Local workspace</span><span>Shift + Enter for a new line</span></div>
    </form>
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
