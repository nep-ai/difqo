import { readFileSync, writeFileSync } from 'node:fs';
import { createInterface } from 'node:readline';

const send = (message) => process.stdout.write(JSON.stringify(message) + '\n');
const responses = createInterface({ input: process.stdin });
const account = { type: 'chatgpt' };

for await (const line of responses) {
  const request = JSON.parse(line);
  const respond = (result) => send({ id: request.id, result });
  switch (request.method) {
    case 'initialize':
      respond({});
      break;
    case 'initialized':
      break;
    case 'account/read':
      respond({ account, requiresOpenaiAuth: true });
      break;
    case 'thread/start':
      if (request.params.cwd !== '/workspace' || request.params.approvalPolicy !== 'never') {
        throw new Error('Unexpected thread scope');
      }
      respond({ thread: { id: 'synthetic-thread' } });
      break;
    case 'turn/start': {
      // Earlier smoke scenarios can save other revisions in the same database.
      const decision = /^Saved project decision \(revision [1-9]\d*\):\n/.exec(request.params.input[0].text);
      if (!decision || request.params.input[0].text.slice(decision[0].length) !== 'Use the line after in greeting.txt.\n\nTask goal:\nReplace the fixture greeting') {
        throw new Error('Unexpected task goal or decision revision');
      }
      const [, note, imageLabel, screenshot, ...extra] = request.params.input;
      if (extra.length || note?.type !== 'text' || !note.text.includes('Synthetic fixture context.') ||
          imageLabel?.type !== 'text' || !imageLabel.text.includes('screenshot.png') ||
          screenshot?.type !== 'image' || !screenshot.url.startsWith('data:image/png;base64,iVBORw0KGgo')) {
        throw new Error('Expected the fixture file and screenshot');
      }
      if (readFileSync('greeting.txt', 'utf8') !== 'before\n') {
        throw new Error('Unexpected source checkout');
      }
      writeFileSync('greeting.txt', 'after\n');
      writeFileSync('untracked.txt', 'new file\n');
      respond({ turn: { id: 'synthetic-turn' } });
      send({ method: 'turn/completed', params: {
        threadId: 'synthetic-thread', turn: { id: 'synthetic-turn', status: 'completed' },
      } });
      break;
    }
    default:
      throw new Error(`Unexpected method ${request.method}`);
  }
}
