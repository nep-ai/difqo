import React, { useEffect, useRef, useState } from 'react'

const actionLabel = { create: 'Approve specialist', update: 'Approve update', disable: 'Disable specialist' }
const outcomeLabel = { approve: 'Approved', decline: 'Declined', revise: 'Changes requested', stale: 'Out of date' }

function draftFor(action, specialistID = 0, revision = 0, config = {}) {
  return {
    id: crypto.randomUUID(), action, specialist_id: specialistID, base_revision: revision,
    name: config.name || '', purpose: config.purpose || '', output: config.output || '',
    projects: config.projects?.filter(project => !config.catalog || config.catalog.some(item => item.id === project.id && item.binding === project.binding)).map(project => project.id) || [],
  }
}

function Configuration({ config, catalog }) {
  return <dl className="specialist-config">
    <dt>Purpose</dt><dd>{config.purpose}</dd>
    <dt>Expected output</dt><dd>{config.output}</dd>
    <dt>Approved operations</dt><dd>{config.access}</dd>
    <dt>Project scope</dt><dd><ul>{config.projects.map(project => {
      const available = catalog.some(item => item.id === project.id && item.binding === project.binding)
      return <li key={project.id}>{project.name} ({project.id} · {project.label}){!available && ' — Binding unavailable'}</li>
    })}</ul></dd>
  </dl>
}

function ScopeChanges({ previous, proposed }) {
  const added = proposed.filter(project => !previous.some(item => item.id === project.id && item.binding === project.binding))
  const removed = previous.filter(project => !proposed.some(item => item.id === project.id && item.binding === project.binding))
  if (!added.length && !removed.length) return <p>Project scope stays the same.</p>
  return <ul>
    {added.map(project => <li key={'add-' + project.id}>Authorize {project.name} ({project.label})</li>)}
    {removed.map(project => <li key={'remove-' + project.id}>Remove access to {project.name} ({project.label})</li>)}
  </ul>
}

function StartRun({ specialist, catalog, start }) {
  const [goal, setGoal] = useState('')
  const [selected, select] = useState('')
  const [id, setID] = useState(() => crypto.randomUUID())
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const eligible = specialist.configuration.projects.filter(project =>
    catalog.some(item => item.id === project.id && item.binding === project.binding))
  const project = eligible.find(item => item.id === selected)
  if (!eligible.length) return <p>Update the approved project bindings before starting a run.</p>

  async function submit(event) {
    event.preventDefault()
    if (busy || !project || !goal.trim()) return
    setBusy(true)
    setError('')
    setStatus('')
    try {
      const task = await start({
        request_id: id, goal, specialist_id: specialist.id, revision: specialist.revision,
        project: project.id, binding: project.binding,
      })
      setGoal('')
      select('')
      setID(crypto.randomUUID())
      setStatus('Run queued as goal ' + task.id + '.')
    } catch (err) {
      setError(err.status ? err.message : 'Couldn’t confirm the start. Retry with the same selection to recover the queued run.')
    } finally {
      setBusy(false)
    }
  }

  return <details className="specialist-record">
    <summary>Run this specialist</summary>
    <form onSubmit={submit}>
      <p>{specialist.configuration.name} · Revision {specialist.revision}</p>
      <p>{specialist.configuration.access}</p>
      <label className="specialist-field">Run project
        <select value={selected} disabled={busy} onChange={event => { select(event.target.value); setID(crypto.randomUUID()) }}>
          <option value="">Choose an approved project</option>
          {eligible.map(item => <option key={item.id} value={item.id}>{item.name} ({item.id} · {item.label})</option>)}
        </select>
      </label>
      <label className="specialist-field">Run goal
        <textarea maxLength={4096} required value={goal} readOnly={busy}
          onChange={event => { setGoal(event.target.value); setID(crypto.randomUUID()) }} />
      </label>
      {error && <p role="alert">{error}</p>}
      <p role="status">{status}</p>
      <button className="primary" disabled={busy || !project || !goal.trim()}>{busy ? 'Starting…' : 'Start run'}</button>
    </form>
  </details>
}

export function Specialists({ request, mark, close, onQueued }) {
  const [state, setState] = useState(null)
  const [draft, setDraft] = useState(null)
  const [inspected, inspect] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')
  const title = useRef(null)
  const name = useRef(null)
  const review = useRef(null)
  const [focusReview, setFocusReview] = useState(false)

  async function load(signal) {
    const response = await request('/specialists', { signal })
    const latest = await response.json()
    setState(latest)
    return latest
  }

  useEffect(() => {
    const controller = new AbortController()
    title.current?.focus()
    load(controller.signal).catch(() => {
      if (!controller.signal.aborted) setError('Couldn’t load specialists. Check the local service and retry.')
    })
    return () => controller.abort()
  }, [])

  useEffect(() => {
    if (draft) name.current?.focus()
  }, [draft?.id && draft.action, draft?.specialist_id])

  useEffect(() => {
    if (focusReview && review.current) {
      review.current.focus()
      setFocusReview(false)
    }
  }, [focusReview, state])

  async function perform(operation) {
    setBusy(true)
    setError('')
    setStatus('')
    try {
      await operation()
    } catch (err) {
      setError(err.status
        ? err.message
        : 'Couldn’t confirm the change. Refresh specialists to check the saved outcome before retrying.')
      try {
        const latest = await load()
        setDraft(current => latest.proposals.some(proposal => proposal.id === current?.id) ? null : current)
      } catch { /* Keep the current review visible while the service is unavailable. */ }
    } finally {
      setBusy(false)
    }
  }

  async function post(path, data) {
    const response = await request(path, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data),
    })
    return response.json()
  }

  function propose(values) {
    return perform(async () => {
      await post('/specialist-proposals', values)
      setDraft(null)
      setStatus('Proposal saved.')
      await load()
      setFocusReview(true)
    })
  }

  function decide(proposal, outcome) {
    return perform(async () => {
      const saved = await post('/specialist-proposals/' + proposal.id + '/decision', { outcome })
      await load()
      setStatus(outcomeLabel[outcome] + '.')
      if (saved.specialist_id) inspect(saved.specialist_id)
      if (outcome === 'revise') {
        setDraft(draftFor(saved.action, saved.specialist_id, saved.base_revision, { ...saved.configuration, catalog: state.projects }))
      } else {
        title.current?.focus()
      }
    })
  }

  function change(field, value) {
    setDraft(current => ({ ...current, [field]: value, id: crypto.randomUUID() }))
  }

  const current = state?.specialists.find(specialist => specialist.id === inspected)

  return <section className="turn chief specialist-turn" aria-labelledby="specialists-title">
    {mark}
    <div className="turn-content">
      <strong className="speaker">Chief of Staff</strong>
      <div className="work-card specialist-card">
        <div className="specialist-actions">
          <h2 id="specialists-title" tabIndex={-1} ref={title}>Specialists</h2>
          <button className="subtle" onClick={close} disabled={busy}>Close specialists</button>
          {state && <button className="subtle" onClick={() => perform(() => load())} disabled={busy}>Refresh specialists</button>}
        </div>
        <p role="status">{status || (!state && !error ? 'Loading specialists…' : '')}</p>
        {error && <p className="submit-error" role="alert">{error}</p>}
        {!state && error && <button className="subtle" onClick={() => perform(() => load())} disabled={busy}>Retry loading specialists</button>}
        {state && <>
          {state.projects.length === 0 && <p>Add projects to the local catalog and restart the service to propose a specialist.</p>}
          <button className="primary" disabled={busy || !state.projects.length || !!draft} onClick={() => setDraft(draftFor('create'))}>
            Propose a specialist
          </button>
          {state.specialists.length > 0 && <label className="specialist-field">Inspect a specialist
            <select value={inspected} onChange={event => inspect(Number(event.target.value))}>
              <option value={0}>Choose a specialist</option>
              {state.specialists.map(item => <option key={item.id} value={item.id}>{item.configuration.name} (#{item.id})</option>)}
            </select>
          </label>}
          {current && <article className="specialist-record">
            <h3>{current.configuration.name}</h3>
            <p>{current.enabled ? 'Enabled' : 'Disabled'} · Revision {current.revision}</p>
            <Configuration config={current.configuration} catalog={state.projects} />
            {current.enabled && <div className="specialist-actions">
              <button className="subtle" disabled={busy || !!draft || !state.projects.length}
                onClick={() => setDraft(draftFor('update', current.id, current.revision, { ...current.configuration, catalog: state.projects }))}>Propose update</button>
              <button className="subtle" disabled={busy || !!draft}
                onClick={() => propose(draftFor('disable', current.id, current.revision))}>Review disable</button>
            </div>}
            {current.enabled && <StartRun key={current.id + ':' + current.revision} specialist={current} catalog={state.projects} start={async input => {
              const task = await post('/specialist-runs', input)
              onQueued(task)
              return task
            }} />}
          </article>}
          {draft && <form className="specialist-record" onSubmit={event => { event.preventDefault(); propose(draft) }}>
            <h3>{draft.action === 'create' ? 'What should this specialist do?' : 'What should change?'}</h3>
            <label className="specialist-field">Specialist name
              <input ref={name} disabled={busy} required maxLength={100} value={draft.name} onChange={event => change('name', event.target.value)} />
            </label>
            <label className="specialist-field">Purpose
              <textarea disabled={busy} required maxLength={4096} value={draft.purpose} onChange={event => change('purpose', event.target.value)} />
            </label>
            <label className="specialist-field">Expected output
              <textarea disabled={busy} required maxLength={1024} value={draft.output} onChange={event => change('output', event.target.value)} />
            </label>
            <fieldset disabled={busy}>
              <legend>Projects to authorize</legend>
              {state.projects.map(project => <label className="specialist-choice" key={project.id}>
                <input type="checkbox" checked={draft.projects.includes(project.id)} onChange={event => change('projects',
                  event.target.checked ? [...draft.projects, project.id] : draft.projects.filter(id => id !== project.id))} />
                {project.name} ({project.id} · {project.label})
              </label>)}
            </fieldset>
            <p>{state.access}</p>
            <div className="specialist-actions">
              <button className="primary" disabled={busy || !state.projects.some(project => draft.projects.includes(project.id))}>Review proposal</button>
              <button type="button" className="subtle" disabled={busy} onClick={() => { setDraft(null); title.current?.focus() }}>Cancel draft</button>
            </div>
          </form>}
          {state.proposals.length > 0 && <div ref={review} tabIndex={-1} className="specialist-record">
            <h3>Specialist proposals</h3>
            {state.proposals.toReversed().map(proposal => {
              const target = state.specialists.find(item => item.id === proposal.specialist_id)
              const pending = !proposal.outcome
              const verb = { create: 'Create', update: 'Update', disable: 'Disable' }[proposal.action]
              return <details className="specialist-proposal" key={proposal.id} open={pending || undefined}>
                <summary>{verb} {proposal.configuration.name} · {outcomeLabel[proposal.outcome] || 'Ready for review'}</summary>
                {proposal.outcome === 'stale' && <p>The configuration or project binding changed. Inspect the current configuration and make a fresh proposal.</p>}
                {proposal.action === 'disable'
                  ? <p>Disabling removes eligibility for every project in this configuration.</p>
                  : proposal.action === 'update' && pending && target && <details>
                    <summary>Compare with current configuration</summary>
                    <p>{target.configuration.name} · Revision {target.revision}</p>
                    <Configuration config={target.configuration} catalog={state.projects} />
                  </details>}
                {proposal.action === 'update' && pending && target && <ScopeChanges
                  previous={target.configuration.projects} proposed={proposal.configuration.projects} />}
                <Configuration config={proposal.configuration} catalog={state.projects} />
                {proposal.outcome === 'approve' && <p>{proposal.action === 'disable'
                  ? 'Disabled by local operator'
                  : `Revision ${proposal.base_revision + 1} approved by local operator`}</p>}
                {pending && <div className="specialist-actions">
                  <button className="primary" disabled={busy} onClick={() => decide(proposal, 'approve')}>{actionLabel[proposal.action]}</button>
                  {proposal.action !== 'disable' && <button className="subtle" disabled={busy || !!draft}
                    onClick={() => decide(proposal, 'revise')}>Request changes</button>}
                  <button className="subtle" disabled={busy} onClick={() => decide(proposal, 'decline')}>
                    {proposal.action === 'disable' ? 'Cancel disable' : 'Decline'}
                  </button>
                </div>}
              </details>
            })}
          </div>}
        </>}
      </div>
    </div>
  </section>
}
