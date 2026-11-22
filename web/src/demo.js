export const sampleGoal = 'Make CSV exports workspace-safe'
export const baseRevision = '141a12b'
export const patchRevision = '79cd8e1'
export const checkCommand = 'go test ./internal/reports -run TestExportWorkspaceIsolation -v'

export const patch = [
  'diff --git a/internal/reports/export.go b/internal/reports/export.go',
  '--- a/internal/reports/export.go',
  '+++ b/internal/reports/export.go',
  '@@ -30,2 +30,2 @@ func Export(ctx context.Context, workspaceID string) error {',
  '-    rows := store.ListReports(ctx)',
  '+    rows := store.ListWorkspaceReports(ctx, workspaceID)',
  '     return writeCSV(rows)',
].join('\n') + '\n'

export const readiness = [
  { id: 'plane', name: 'Control plane', description: 'Can coordinate and save requests.', problem: 'The control plane is still starting.', action: 'Simulate control plane recovery' },
  { id: 'database', name: 'PostgreSQL', description: 'Can save work and recover it after restart.', problem: 'The configured database did not respond.', action: 'Simulate database recovery' },
  { id: 'worker', name: 'Agent execution', description: 'Can start an isolated checkout.', problem: 'The worker could not start an isolated container.', action: 'Simulate runtime recovery' },
  { id: 'codex', name: 'Codex', description: 'The provider probe passed in the execution environment.', problem: 'Sign in to Codex in the execution environment.', action: 'Simulate Codex sign-in' },
]

const key = 'difqo-local-ui-demo-v1'

export function freshDemo() {
  return { stage: 'install', blocker: '', messages: [], goals: {}, approvals: {}, counter: 0 }
}

export function loadDemo() {
  try {
    const saved = JSON.parse(localStorage.getItem(key))
    if (saved) {
      for (const goal of Object.values(saved.goals)) {
        if (goal.status === 'Running') goal.status = 'Paused'
        if (goal.check === 'Checking') goal.check = 'Required'
      }
      return saved
    }
  } catch {
    // Start fresh when browser storage is unavailable or invalid.
  }
  return freshDemo()
}

export function saveDemo(demo) {
  try {
    localStorage.setItem(key, JSON.stringify(demo))
  } catch {
    // The demo still works for the current tab when storage is unavailable.
  }
}

export function answer(demo, text) {
  const messages = [...demo.messages, { role: 'user', text }]
  const say = (reply, attachment = {}) => ({ ...demo, messages: [...messages, { role: 'chief', text: reply, ...attachment }] })

  if (/^(what|how).*(status|blocking|progress)|^status\??$/i.test(text)) {
    const latest = Object.values(demo.goals).at(-1)
    return latest
      ? say('Here’s the latest on “' + latest.title + '.”', { goal: latest.id })
      : say('There’s no work underway yet. Tell me what you’d like to accomplish.')
  }

  if (/^(connect|link|add)\s+(github|slack|an? mcp server|an? skill|an? plugin)\b/i.test(text) ||
      /^((create|add|set up|draft)\s+.*(specialist|agent))\b/i.test(text)) {
    const request = text.match(/github|slack|mcp server|skill|plugin/i)?.[0].toLowerCase() || 'specialist'
    const id = String(demo.counter + 1)
    const scopes = {
      github: 'Link a sample identity to orchard/reports. External posts and pull requests still require a separate decision.',
      slack: 'Link a sample workspace and one private DM for requests and updates.',
      skill: 'Allow the sample verification procedure to use tools already granted for this goal.',
      plugin: 'Allow the sample evidence viewer to read local artifacts.',
      'mcp server': 'Allow the sample documentation server to read the selected project documentation.',
      specialist: 'Allow a proposed specialist to read the selected repository and run local checks in isolation for up to ten minutes.',
    }
    return {
      ...say('Here’s the access I’d need for that. Please review it before I continue.', { approval: id }),
      counter: Number(id),
      approvals: { ...demo.approvals, [id]: { request, scope: scopes[request], status: 'Pending' } },
    }
  }

  const id = String(demo.counter + 1)
  const goal = { id, title: text, sample: text === sampleGoal, status: 'Proposed', check: 'Required' }
  return {
    ...say('I’ll scope the work, coordinate a specialist, and report back with evidence. Here’s what I can do in this workspace.', { goal: id }),
    counter: Number(id),
    goals: { ...demo.goals, [id]: goal },
  }
}
