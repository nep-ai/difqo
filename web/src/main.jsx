import React, { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/geist/wght.css'
import '@fontsource/barlow-condensed/latin-600.css'
import { Specialists } from './specialists.jsx'
import { Connections, RunSample } from './connections.jsx'
import './style.css'

const api = import.meta.env.DEV ? '/api' : ''
const base = import.meta.env.BASE_URL
const sampleGoal = 'In greeting.txt, replace the line before with the line after. Leave other files unchanged.'
const mac = /Mac/.test(navigator.platform)
const sandboxGuide = 'https://github.com/nep-ai/difqo/blob/main/docs/development.md#runtime-selection-and-linux-sandbox-setup'
const statuses = {
  queued: 'Queued',
  claimed: 'Running',
  completed: 'Completed',
  blocked: 'Blocked',
  exhausted: 'Attempt limit reached',
  interrupted: 'Lease expired',
  failed: 'Failed',
}

function Mark() {
  return <img className="mark" src={base + 'favicon.svg'} alt="" />
}

function macWorkerCommand(action) {
  return 'service=$(/usr/bin/plutil -extract ProgramArguments.1 raw -o - "$HOME/Library/LaunchAgents/com.difqo.server.plist") && '
    + '/bin/bash "${service%/*}/first-goal-macos" ' + action
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
      {download && <a className="download" href={api + endpoint} download={'difqo-' + id + '.diff'}>Download diff</a>}
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

  let status = 'Rerun the check to approve these changes.'
  if (check?.status === 'failed') status = 'Rerun failed (exit ' + check.exit_code + ').'
  if (check?.status === 'interrupted') status = 'Rerun interrupted. Run the check again.'
  if (check?.status === 'passed') status = 'Rerun passed.'
  if (approved) status = 'Changes approved.'
  if (review && !review.patch_available) status = 'The saved diff is missing or has changed.'
  if (running) status = 'Checking the saved changes against the original base…'
  if (!review && !busy) status = ''

  return <details className="review" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>Review changes</summary>
    <dl className="facts review-facts">
      <dt>Project decision</dt><dd>{goal.decision ? <>Revision {goal.decision.id}<p>{goal.decision.body}</p></> : 'No decision was saved for this goal.'}</dd>
      <dt>Base commit</dt><dd><code>{snapshot.base}</code></dd>
      {snapshot.candidate && <><dt>Checked candidate</dt><dd><code>{snapshot.candidate}</code></dd></>}
      <dt>Diff SHA-256</dt><dd><code>{snapshot.patch_sha256}</code></dd>
      <dt>Check command</dt><dd><code>{snapshot.command}</code></dd>
      <dt>Original check</dt><dd>{result.exit_code === 0 ? 'Passed' : 'Exit ' + result.exit_code}</dd>
    </dl>
    <Evidence id={goal.id} file="change.diff" label="View diff" download />
    <Evidence id={goal.id} file="check.log" label="View original check log" />
    {result.exit_code !== 0 && <Evidence id={goal.id} file="execution.log" label="View execution diagnostic" />}
    {check?.output && !running && <details>
      <summary>View rerun log</summary>
      <pre className="output" tabIndex={0} aria-label="Rerun log">{check.output}</pre>
    </details>}
    {open && !review && !busy && !loadFailed && <p role="status">Loading review…</p>}
    {status && <p role="status" className="review-status">{status}</p>}
    {loadFailed && <p role="alert">Couldn’t load the review.</p>}
    {actionError && <p role="alert">{actionError}</p>}
    <div className="actions">
      {!review && loadFailed && <button onClick={() => { setLoadFailed(false); setRefresh(value => value + 1) }}>Refresh review</button>}
      {review?.patch_available && !approved && <>
        <button disabled={running || Boolean(busy)} onClick={() => act('rerun')}>Rerun check</button>
        <button className="primary" disabled={running || Boolean(busy) || check?.status !== 'passed'} onClick={() => act('approve')}>Approve changes</button>
      </>}
    </div>
    {review?.patch_available && !approved && <p>Approval records your review. Download the diff to apply the changes manually.</p>}
  </details>
}

function ProjectDecision({ dialog }) {
  const [body, setBody] = useState('')
  const [projects, setProjects] = useState([])
  const [scope, setScope] = useState('')
  const [revision, setRevision] = useState(null)
  const [loading, setLoading] = useState(false)
  const [ready, setReady] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const pendingLoad = useRef(null)

  function decisionPath(project) {
    return project ? '/project-decision?' + new URLSearchParams({ project: project.id, binding: project.binding }) : '/project-decision'
  }

  async function load(project, open = false) {
    pendingLoad.current?.abort()
    const controller = new AbortController()
    pendingLoad.current = controller
    if (open) dialog.current.showModal()
    setScope(project?.id ?? '')
    setLoading(true)
    setReady(false)
    setError('')
    try {
      if (open) {
        const catalog = await request('/specialist-projects', { signal: controller.signal })
        setProjects(await catalog.json())
      }
      const response = await request(decisionPath(project), { signal: controller.signal })
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
      await request(decisionPath(projects.find(project => project.id === scope)), {
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
    <button type="button" className="decision-trigger" onClick={() => load(null, true)}>Project decision</button>
    <dialog ref={dialog} aria-labelledby="decision-title" onCancel={event => {
      if (saving) event.preventDefault()
      else pendingLoad.current?.abort()
    }}>
      <h2 id="decision-title">Project decision</h2>
      <p>New runs use the latest saved decision for their project.</p>
      {projects.length > 0 && <label className="specialist-field">Decision scope
        <select value={scope} disabled={saving || loading} onChange={event => load(projects.find(project => project.id === event.target.value))}>
          <option value="">Ordinary goals</option>
          {projects.map(project => <option key={project.id} value={project.id}>{project.name} ({project.id})</option>)}
        </select>
      </label>}
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

function AttemptHistory({ goal }) {
  const [open, setOpen] = useState(false)
  const [history, setHistory] = useState(null)
  const [error, setError] = useState(false)
  const [refresh, retry] = useState(0)

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    let timer
    async function load() {
      try {
        const response = await request('/specialist-runs/' + goal.id + '/attempts', { signal: controller.signal })
        const data = await response.json()
        if (controller.signal.aborted) return
        setHistory(data)
        setError(false)
      } catch {
        if (!controller.signal.aborted) setError(true)
      } finally {
        if (!controller.signal.aborted && (goal.status === 'queued' || goal.status === 'claimed')) {
          timer = setTimeout(load, 3000)
        }
      }
    }
    load()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [goal.id, goal.status, goal.attempt?.id, open, refresh])

  return <details className="review" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>View attempt history</summary>
    {error && <p role="alert">Couldn’t load attempts. <button className="subtle" onClick={() => retry(value => value + 1)}>Retry</button></p>}
    {open && !history && !error && <p role="status">Loading attempts…</p>}
    {history && <>
      {!history.claimable && goal.status !== 'completed' && <p>Project authorization has changed. This run cannot be claimed again.</p>}
      {history.attempts.length === 0 && <p>No attempts have been recorded.</p>}
      {history.attempts.map(attempt => <details key={attempt.id}>
        <summary>Attempt {attempt.number} · {statuses[attempt.status] ?? attempt.status}</summary>
        {attempt.status === 'interrupted' && <p>The worker did not report a result before its lease expired.</p>}
        {attempt.result && <><p>Exit code {attempt.result.exit_code}</p><pre className="specialist-report">{attempt.result.output}</pre></>}
      </details>)}
    </>}
  </details>
}

function ProjectFile({ goal, openSetup }) {
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
    {goal.specialist_run && <p>{goal.specialist_run.project.name} ({goal.specialist_run.project.id}) · {goal.specialist_run.name} · Revision {goal.specialist_run.revision}</p>}
    {goal.specialist_run && <p>{goal.decision ? 'Saved decision revision ' + goal.decision.id : 'No saved decision for this project.'}</p>}
    {goal.status === 'queued' && <p>{goal.specialist_run ? 'Waiting for a worker bound to this project.' : 'Goal saved. Work starts when a local worker claims it.'}</p>}
    {mac && goal.status === 'queued' && goal.goal === sampleGoal && !goal.specialist_run &&
      Number.isSafeInteger(goal.id) && goal.id > 0 &&
      <RunSample id={goal.id} request={request} openSetup={openSetup} fallback={<div>
        <p>Run this sample goal in a disposable repository from Terminal.</p>
        <pre className="worker-command">{macWorkerCommand('run ' + goal.id)}</pre>
      </div>} />}
    {goal.status === 'claimed' && <p>The local worker is running this goal.</p>}
    {goal.status === 'blocked' && <p>Restore Codex sign-in in the worker’s execution environment, then submit the goal again.</p>}
    {goal.status === 'exhausted' && <p>Check that a local worker is available before submitting this goal again.</p>}
    {result && !goal.specialist_run && <dl className="facts">
      <dt>Worker result</dt><dd>{result.output || 'No output was recorded.'}</dd>
      <dt>Exit code</dt><dd><code>{result.exit_code}</code></dd>
    </dl>}
    {goal.specialist_run && <AttemptHistory goal={goal} />}
    {result?.snapshot && <PatchReview goal={goal} key={goal.attempt.id} />}
    {result?.artifacts && !result.snapshot && <details className="review" key={goal.attempt.id}>
      <summary>View run evidence</summary>
      <Evidence id={goal.id} file="change.diff" label="View diff" download />
      <Evidence id={goal.id} file="check.log" label="View logs" />
      {failed && <Evidence id={goal.id} file="execution.log" label="View execution diagnostic" />}
    </details>}
  </section>
}

function encodeAttachment(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve({ name: file.name, data: String(reader.result).split(',', 2)[1] })
    reader.onerror = () => reject(new Error('Couldn’t read ' + file.name + '. Attach it again.'))
    reader.readAsDataURL(file)
  })
}

function CodingTurn({ goal, openSetup }) {
  return <>
    <article className="turn user" aria-label="You"><div className="turn-content">
      <p>{goal.goal}</p>
      {goal.attachments?.length > 0 && <ul className="sent-files" aria-label="Attachments">
        {goal.attachments.map((file, index) => <li key={index}>{file.name}</li>)}
      </ul>}
    </div></article>
    <article className="turn chief" aria-label="Chief of Staff"><Mark /><div className="turn-content">
      <strong className="speaker">Chief of Staff</strong><ProjectFile goal={goal} openSetup={openSetup} />
    </div></article>
  </>
}

function ChiefTurn({ turn }) {
  return <>
    <article className="turn user" aria-label="You"><div className="turn-content">
      {turn.text && <p>{turn.text}</p>}
      {turn.attachments?.length > 0 && <ul className="sent-files" aria-label="Attachments">
        {turn.attachments.map((file, index) => <li key={index}>{file.name}</li>)}
      </ul>}
    </div></article>
    <article className="turn chief" aria-label="Chief of Staff"><Mark /><div className="turn-content">
      <strong className="speaker">Chief of Staff</strong>
      {turn.state === 'running' && <p role="status">Thinking…</p>}
      {turn.state === 'attention' && <p role="status">{turn.problem}</p>}
      {turn.state === 'completed' && <p>{turn.reply}</p>}
    </div></article>
  </>
}

function Chat({ goals, chiefTurns, chiefAvailable, connection, submit, addTask, setupDialog, setupOpen, openSetup, closeSetup }) {
  const [draft, setDraft] = useState('')
  const [codingGoal, setCodingGoal] = useState(false)
  const requestID = useRef(null)
  const [files, setFiles] = useState([])
  const filePicker = useRef(null)
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
  const latestChief = chiefTurns.at(-1)
  const useChief = chiefAvailable === true && !codingGoal
  const chiefBusy = useChief && latestChief?.state === 'running'

  useEffect(() => {
    if ((latest || latestChief) && (!openedHistory.current || submitted.current)) {
      openedHistory.current = true
      end.current?.scrollIntoView({ block: 'nearest' })
      if (submitted.current) composer.current?.focus({ preventScroll: true })
      submitted.current = false
    }
  }, [latest, latestChief?.id])

  function attach(selected) {
    if (sending || selected.length === 0) return
    const combined = [...files, ...selected]
    if (combined.length > 4 || combined.some(file => !file.size || file.size > 2 * 1024 ** 2) ||
      combined.reduce((total, file) => total + file.size, 0) > 4 * 1024 ** 2) {
      setError('Attach up to four non-empty files, 2 MB each and 4 MB total.')
      return
    }
    setError('')
    setFiles(combined)
  }

  async function send(event) {
    event.preventDefault()
    if (sending || chiefBusy || chiefAvailable === null || connection !== 'ready') return
    if (!draft.trim() && (!useChief || files.length === 0)) {
      if (files.length) setError('What would you like Chief of Staff to do with these files?')
      return
    }
    const goal = draft.trim()
    setSending(true)
    setError('')
    try {
      submitted.current = true
      if (!requestID.current || requestID.current.goal !== goal || requestID.current.files !== files ||
        requestID.current.useChief !== useChief) {
        requestID.current = { id: crypto.randomUUID(), goal, files, useChief }
      }
      await submit(goal, await Promise.all(files.map(encodeAttachment)), useChief, requestID.current.id)
      requestID.current = null
      setDraft('')
      setFiles([])
      setCodingGoal(false)
    } catch (err) {
      submitted.current = false
      setError(err instanceof RequestError && [400, 409].includes(err.status)
        ? err.message
        : 'Couldn’t confirm submission. Check the conversation before retrying. Your draft is still here.')
    } finally {
      setSending(false)
    }
  }

  return <main className="chat" aria-labelledby="conversation-title">
    <h1 id="conversation-title" className="sr-only">Chief of Staff conversation</h1>
    <details className="chat-tools">
      <summary>Project tools</summary>
      <div className="actions">
        <ProjectDecision dialog={decisionDialog} />
        {chiefAvailable && <button className="subtle" type="button" aria-pressed={codingGoal} disabled={sending}
          onClick={() => { setCodingGoal(value => !value); composer.current?.focus() }}>Queue a coding goal</button>}
        <button className="subtle" type="button" ref={specialistTrigger} aria-expanded={showSpecialists}
          onClick={() => setShowSpecialists(open => !open)}>Specialists</button>
      </div>
    </details>
    {goals.length || chiefTurns.length ? <section className="conversation" aria-label="Conversation">
      {[
        ...goals.map(goal => ({ item: goal, kind: 'goal' })),
        ...chiefTurns.map(turn => ({ item: turn, kind: 'chat' })),
      ].sort((a, b) => Date.parse(a.item.created_at) - Date.parse(b.item.created_at)).map(({ item, kind }) =>
        kind === 'goal'
          ? <CodingTurn key={'goal-' + item.id} goal={item} openSetup={openSetup} />
          : <ChiefTurn key={'chat-' + item.id} turn={item} />)}
      <div ref={end} />
    </section> : <section className="welcome">
      <Mark />
      <h2>{chiefAvailable ? 'How can I help?' : 'Run your first coding goal'}</h2>
      <p>{chiefAvailable ? 'Write a message or attach a file to get started.' : 'Set up a local Codex worker to run a coding goal and review its patch here.'}</p>
      <button className="subtle" type="button" onClick={openSetup}>Set up connections</button>
    </section>}
    {showSpecialists && <Specialists request={request} mark={<Mark />} onQueued={addTask} close={() => {
      setShowSpecialists(false)
      specialistTrigger.current?.focus()
    }} />}
    <div className="composer">
      <form onSubmit={send}>
        {error && <p className="submit-error" role="alert">{error}</p>}
        <div className="compose-box">
          {files.length > 0 && <ul className="compose-files" aria-label="Attachments">
            {files.map((file, index) => <li key={index}>{file.name}
              <button type="button" className="subtle" aria-label={'Remove ' + file.name} disabled={sending}
                onClick={() => { setFiles(files.filter((_, position) => position !== index)); setError('') }}>Remove</button>
            </li>)}
          </ul>}
          <label htmlFor="message" className="sr-only">Message Chief of Staff</label>
          <textarea id="message" ref={composer} rows={2} maxLength={4096} value={draft}
            onChange={event => setDraft(event.target.value)} readOnly={sending} placeholder={codingGoal ? 'Describe a repository change' : 'Message Chief of Staff'}
            onPaste={event => {
              const pasted = Array.from(event.clipboardData.files)
              if (pasted.length) {
                event.preventDefault()
                attach(pasted)
              }
            }}
            onKeyDown={event => {
              if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault()
                event.currentTarget.form.requestSubmit()
              }
            }} />
          <div className="compose-actions">
            <input ref={filePicker} type="file" multiple hidden onChange={event => {
              attach(Array.from(event.target.files))
              event.target.value = ''
            }} />
            <button type="button" className="subtle" disabled={sending} onClick={() => filePicker.current.click()}>Attach</button>
            <button className="primary" disabled={(!draft.trim() && files.length === 0) || sending || chiefBusy || chiefAvailable === null || connection !== 'ready'}>{sending ? 'Sending…' : 'Send'}</button>
          </div>
        </div>
      </form>
    </div>
    <Connections dialog={setupDialog} open={setupOpen} onClose={closeSetup} request={request}
      sandboxGuide={sandboxGuide} sampleDisabled={sending || !!draft.trim() || files.length > 0} onSample={() => {
        setDraft(sampleGoal)
        setCodingGoal(true)
        setupDialog.current.close()
        composer.current?.focus()
      }} />
  </main>
}

function App() {
  const [connection, setConnection] = useState('checking')
  const [goals, setGoals] = useState([])
  const [chiefTurns, setChiefTurns] = useState([])
  const [chiefAvailable, setChiefAvailable] = useState(null)
  const generation = useRef(0)
  const setupDialog = useRef(null)
  const [setupOpen, setSetupOpen] = useState(false)
  function openSetup() {
    setSetupOpen(true)
    setupDialog.current.showModal()
  }

  useEffect(() => {
    const controller = new AbortController()
    let timer
    async function refresh() {
      const current = generation.current
      try {
        const [response, conversation] = await Promise.all([
          request('/tasks', { signal: controller.signal }),
          request('/chief/conversation', { signal: controller.signal }).catch(error => {
            if (error instanceof RequestError && error.status === 404) return null
            throw error
          }),
        ])
        const recent = await response.json()
        const turns = conversation ? await conversation.json() : []
        if (!Array.isArray(recent) || !Array.isArray(turns)) throw new Error('Invalid task history')
        if (!controller.signal.aborted && current === generation.current) {
          setGoals(recent.toReversed())
          setChiefTurns(turns.toReversed())
          setChiefAvailable(!!conversation)
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

  async function submit(goal, attachments, useChief, requestID) {
    const response = await request(useChief ? '/chief/conversation' : '/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(useChief ? { text: goal, attachments, request_id: requestID } : { goal, attachments }),
    })
    const saved = await response.json()
    if (useChief) {
      generation.current++
      setChiefTurns(current => current.some(turn => turn.id === saved.id) ? current : [...current, saved].slice(-100))
    } else {
      addTask(saved)
    }
  }

  function addTask(saved) {
    generation.current++
    setGoals(current => current.some(item => item.id === saved.id) ? current : [...current, saved].slice(-100))
  }

  return <div className="app">
    <header className="topbar"><div className="brand"><Mark /><span>difqo<span className="wordmark-period">.</span></span></div>
      <span className="surface-title">Chief of Staff</span>
      <div className="top-actions">
        <span className={'badge ' + (connection === 'ready' ? 'good' : connection === 'offline' ? 'attention' : '')} role="status">
          {{ ready: 'Local service online', checking: 'Connecting to service', offline: 'Service unavailable' }[connection]}
        </span>
        <button className="subtle" onClick={openSetup}>Connections</button>
      </div>
    </header>
    {connection === 'offline' && <p className="connection-note" role="status">Couldn’t reach the local service. Check that Difqo and PostgreSQL are running. This page reconnects automatically.</p>}
    <Chat goals={goals} chiefTurns={chiefTurns} chiefAvailable={chiefAvailable} connection={connection} submit={submit} addTask={addTask} setupDialog={setupDialog}
      setupOpen={setupOpen} openSetup={openSetup} closeSetup={() => setSetupOpen(false)} />
  </div>
}

createRoot(document.getElementById('root')).render(<App />)
