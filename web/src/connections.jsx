import React, { useEffect, useState } from 'react'

function useConnection(request, path, open, interval = 0) {
  const [view, setView] = useState(null)
  const [busy, setBusy] = useState(false)
  const [readError, setReadError] = useState('')
  const [actionError, setActionError] = useState('')
  const [retry, setRetry] = useState(0)

  useEffect(() => {
    if (!open || busy) return
    const controller = new AbortController()
    let timer
    async function load() {
      try {
        const response = await request(path, { signal: controller.signal })
        const data = await response.json()
        if (!controller.signal.aborted) {
          setView(current => ({ ...data, imported: current?.imported }))
          setReadError('')
        }
      } catch (err) {
        if (!controller.signal.aborted) {
          if (err.status === 404) {
            setView({ unavailable: true })
            setReadError('')
          } else {
            setReadError('Couldn’t read the connection status. Try again.')
          }
        }
      } finally {
        if (interval && !controller.signal.aborted) timer = setTimeout(load, interval)
      }
    }
    load()
    return () => {
      controller.abort()
      clearTimeout(timer)
    }
  }, [request, path, open, busy, interval, retry])

  async function act(action, extra = {}, timeoutMs = 12000, target = path) {
    if (busy) return null
    setBusy(true)
    setActionError('')
    try {
      const response = await request(target, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(action ? { action, ...extra } : extra),
        timeoutMs,
      })
      const data = await response.json()
      setView(data)
      return data
    } catch (err) {
      setActionError(err?.message && err?.status !== 502 ? err.message
        : action === 'begin' ? 'Couldn’t start Slack setup. Check the pilot code and try again.'
          : 'Couldn’t complete this action. Check the connection and try again.')
      return null
    } finally {
      setBusy(false)
    }
  }

  return {
    view, busy, error: readError || actionError, readError, actionError, act,
    reload: () => { setReadError(''); setRetry(value => value + 1) },
  }
}

function ConnectionCard({ title, children }) {
  return <section className="connection-card">
    <h3>{title}</h3>
    {children}
  </section>
}

function ConnectionError({ connection }) {
  return <>
    {connection.readError && <p className="submit-error" role="alert">
      {connection.readError} <button onClick={connection.reload} disabled={connection.busy}>Retry</button>
    </p>}
    {connection.actionError && <p className="submit-error" role="alert">{connection.actionError}</p>}
  </>
}

function ChatGPT({ request, open, sandboxGuide, onSample, sampleDisabled }) {
  const connection = useConnection(request, '/setup/codex', open, 1200)
  const { view, busy, act } = connection
  const pending = ['preparing', 'authorize', 'running_sample'].includes(view?.state)
  return <ConnectionCard title="ChatGPT">
    <p>Use your ChatGPT account to run Codex. Open Docker Desktop before signing in.</p>
    <ConnectionError connection={connection} />
    {!view && !connection.error && <p role="status">Checking setup…</p>}
    {view?.unavailable && <p>In-app sign-in is available on the installed Apple silicon build. <a href={sandboxGuide}>Local worker setup</a></p>}
    {view?.state === 'preparing' && <p role="status">Checking the worker and preparing Codex…</p>}
    {view?.state === 'running_sample' && <p role="status">The Codex worker is starting the sample goal.</p>}
    {view?.state === 'authorize' && <div className="device-code" role="status">
      <p>Open ChatGPT sign-in and enter this one-time code.</p>
      <code>{view.code}</code>
      <a href={view.url} target="_blank" rel="noreferrer">Open ChatGPT sign-in</a>
    </div>}
    {view?.state === 'attention' && <p role="status">{view.message} <a href={sandboxGuide}>Worker setup help</a></p>}
    {view?.state === 'unknown' && <p>Sign in or check whether a saved worker is ready.</p>}
    {view?.state === 'ready' && <p role="status">ChatGPT connected. Codex worker is ready.</p>}
    {view && !view.unavailable && !pending && <div className="actions">
      {view.state !== 'ready' && <button type="button" className="primary" disabled={busy} onClick={() => act('prepare')}>Sign in with ChatGPT</button>}
      <button type="button" disabled={busy} onClick={() => act('check')}>Check worker status</button>
      {view.state === 'ready' && <button type="button" className="primary" disabled={sampleDisabled} onClick={onSample}>Load sample goal</button>}
    </div>}
    {view?.state === 'ready' && sampleDisabled && <p>Clear or queue your current draft to load the sample.</p>}
  </ConnectionCard>
}

const slackStatus = {
  disconnected: 'Slack isn’t connected.',
  expired: 'The pairing expired. Start setup again.',
  connecting: 'Slack authorization is pending.',
  approval_needed: 'Workspace approval is pending.',
  installed: 'The app is installed. Check your authorization to continue.',
  ready_to_link: 'Slack authorized. Confirm your workspace and operator.',
  claiming: 'Linking your Slack DM…',
  claim_rejected: 'Slack rejected this pairing. Disconnect and start setup again.',
  connected: 'Slack connected.',
  reconnecting: 'Slack is reconnecting to the relay.',
  reconnect_required: 'Slack needs to be connected again.',
  disconnecting: 'Disconnecting Slack…',
}

function Slack({ request, open }) {
  const connection = useConnection(request, '/slack/connection', open, 2000)
  const { view, busy, act } = connection
  const [code, setCode] = useState('')
  useEffect(() => { if (!open) setCode('') }, [open])
  const [link, setLink] = useState(null)
  const [enrolledPairing, setEnrolledPairing] = useState('')
  const state = view?.state
  const activePair = ['connecting', 'approval_needed', 'installed'].includes(state)
  const canBegin = ['disconnected', 'expired', 'reconnect_required'].includes(state)
  const linkAction = view?.verified_operator ? 'start_install' : 'start_identity'
  const currentLink = link?.pairing === view?.pairing_id && link?.action === linkAction ? link.url : ''

  async function perform(action, data = {}) {
    setLink(null)
    const result = await act(action, { pairing_id: view?.pairing_id, generation: view?.generation, ...data })
    if (action === 'begin' && result?.pairing_id) setEnrolledPairing(result.pairing_id)
    if (result?.oauth_url) setLink({ url: result.oauth_url, pairing: result.pairing_id, action })
    return result
  }

  return <ConnectionCard title="Slack">
    <p>Link a private Slack DM to Chief of Staff. Messages and replies go through the Difqo relay.</p>
    <ConnectionError connection={connection} />
    {!view && !connection.error && <p role="status">Checking connection…</p>}
    {view?.unavailable && <p>Slack setup is available in the installed pilot or when the local service has a Slack relay configured.</p>}
    {state && <p role="status">{slackStatus[state] || 'Check the Slack connection to continue.'}</p>}
    {(canBegin || (activePair && view.requires_code && !view.verified_operator && enrolledPairing !== view.pairing_id)) && <form className="connection-form" onSubmit={event => {
      event.preventDefault()
      const enrollmentCode = code.trim()
      setCode('')
      perform('begin', { code: enrollmentCode })
    }}>
      {view.requires_code && <label>Slack pilot enrollment code
        <input type="password" autoComplete="off" spellCheck={false} value={code} onChange={event => setCode(event.target.value)} maxLength={64} required />
      </label>}
      <button className={canBegin ? 'primary' : 'subtle'} disabled={busy || (view.requires_code && !code.trim())}>{canBegin ? 'Connect Slack' : 'Retry enrollment'}</button>
    </form>}
    {activePair && <div className="actions">
      {state !== 'installed' && (currentLink
        ? <a href={currentLink} target="_blank" rel="noreferrer">{view.verified_operator ? 'Open Slack app installation' : 'Open Slack sign-in'}</a>
        : <button disabled={busy} onClick={() => perform(linkAction)}>{view.verified_operator ? 'Install Slack app' : 'Authorize Slack account'}</button>)}
      {view.verified_operator && !view.installed_workspace && state !== 'approval_needed' &&
        <button disabled={busy} onClick={() => perform('approval_needed')}>I need admin approval</button>}
      <button disabled={busy} onClick={() => perform('refresh')}>Check authorization</button>
    </div>}
    {(view?.binding || view?.verified_workspace) && <p>Workspace <code>{view.binding?.workspace_id || view.verified_workspace}</code>
      {' · '}Operator <code>{view.binding?.operator_id || view.verified_operator}</code></p>}
    {state === 'ready_to_link' && <button className="primary" disabled={busy}
      onClick={() => perform('confirm', { binding: view.binding })}>Link Slack DM</button>}
    {((activePair && (view.verified_operator || enrolledPairing === view.pairing_id)) ||
      ['ready_to_link', 'connected', 'claim_rejected', 'reconnecting'].includes(state)) &&
      <button className="subtle connection-secondary" disabled={busy} onClick={() => perform('disconnect')}>Disconnect Slack</button>}
    {state === 'connected' && <p>{view.plain_text
      ? 'Send a coding goal as plain text in the linked DM.'
      : <>Start your message with <code>goal </code> to submit a coding goal.</>}
      {' '}The queued goal appears in this conversation.</p>}
  </ConnectionCard>
}

function GitHub({ request, open }) {
  const connection = useConnection(request, '/setup/github', open)
  const { view, busy, act } = connection
  const [code, setCode] = useState('')
  useEffect(() => { if (!open) setCode('') }, [open])
  const [reconnecting, setReconnecting] = useState(false)
  return <ConnectionCard title="GitHub">
    <p>Import mentions from the repository approved for the official Difqo GitHub App pilot.</p>
    <ConnectionError connection={connection} />
    {!view && !connection.error && <p role="status">Checking connection…</p>}
    {view?.unavailable && <p>In-app GitHub setup is available in the installed pilot.</p>}
    {view?.repository && <>
      <p role="status">GitHub connected to <a href={'https://github.com/' + view.repository} target="_blank" rel="noreferrer">{view.repository}</a>.</p>
      <p>Post a comment beginning with <code>@difqo</code> in an issue or pull request, then sync here.</p>
      <div className="actions">
        <button className="primary" disabled={busy} onClick={() => act('sync', {}, 57000)}>{busy ? 'Working…' : 'Sync GitHub mentions'}</button>
        <button disabled={busy} onClick={() => setReconnecting(value => !value)}>{reconnecting ? 'Cancel reconnect' : 'Reconnect GitHub'}</button>
        <button className="subtle" disabled={busy} onClick={() => act('disconnect').then(result => result && setReconnecting(false))}>Disconnect GitHub</button>
      </div>
      {Number.isInteger(view.imported) && <p role="status">{view.imported} {view.imported === 1 ? 'task' : 'tasks'} imported.</p>}
    </>}
    {view && !view.unavailable && (!view.repository || reconnecting) && <form className="connection-form" onSubmit={event => {
      event.preventDefault()
      const proof = code.trim()
      setCode('')
      act('connect', { code: proof }).then(result => result && setReconnecting(false))
    }}>
      <p>Open the official app to install it on the approved repository. Then enter the connection code supplied with your pilot invitation.</p>
      <a href="https://github.com/apps/difqo" target="_blank" rel="noreferrer">Open Difqo on GitHub</a>
      <label>GitHub pilot connection code
        <input type="password" autoComplete="off" spellCheck={false} value={code} onChange={event => setCode(event.target.value)} maxLength={64} required />
      </label>
      <button className="primary" disabled={busy || !code.trim()}>Connect GitHub</button>
    </form>}
  </ConnectionCard>
}

export function RunSample({ id, request, fallback, openSetup }) {
  const connection = useConnection(request, '/setup/codex', true, 1500)
  const { view, busy, act } = connection
  if (view?.unavailable) return fallback
  const pending = ['preparing', 'authorize', 'running_sample'].includes(view?.state)
  return <div className="sample-actions">
    <ConnectionError connection={connection} />
    {view?.state === 'attention' && view.task_id === id && <p role="alert">{view.message}</p>}
    {view?.state === 'running_sample' && view.task_id === id && <p role="status">Starting the sample worker…</p>}
    <div className="actions">
      <button className="primary" disabled={!view || busy || pending}
        onClick={() => act(null, {}, 8000, '/setup/codex/sample/' + id)}>Run sample goal</button>
      <button className="subtle" onClick={openSetup}>ChatGPT setup</button>
    </div>
  </div>
}

export function Connections({ dialog, open, onClose, request, sandboxGuide, onSample, sampleDisabled }) {
  return <dialog ref={dialog} className="connections-dialog" aria-labelledby="connections-title" onClose={onClose}>
    <div className="connections-heading">
      <h2 id="connections-title">Connections</h2>
      <form method="dialog"><button className="subtle">Close</button></form>
    </div>
    <div className="connection-cards">
      <ChatGPT request={request} open={open} sandboxGuide={sandboxGuide} onSample={onSample} sampleDisabled={sampleDisabled} />
      <Slack request={request} open={open} />
      <GitHub request={request} open={open} />
    </div>
  </dialog>
}
