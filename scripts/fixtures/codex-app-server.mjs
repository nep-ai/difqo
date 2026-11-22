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
    case 'turn/start':
      if (request.params.input[0].text !== 'Replace the fixture greeting') {
        throw new Error('Unexpected task goal');
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
    default:
      throw new Error(`Unexpected method ${request.method}`);
  }
}
