package execution

import (
	"io"
	"strings"
	"testing"
)

func TestCodexConversationCapturesReplyFromCurrentTurn(t *testing.T) {
	replies := strings.NewReader(`{"id":1,"result":{}}
{"id":2,"result":{"account":{"type":"chatgpt"}}}
{"id":3,"result":{"thread":{"id":"fixture-thread"}}}
` + strings.Repeat(`{"method":"item/completed","params":{"threadId":"fixture-thread","turnId":"fixture-turn","item":{"type":"agentMessage","text":"A quick note.","phase":"commentary"}}}
`, 32) + `{"id":4,"result":{"turn":{"id":"fixture-turn"}}}
{"method":"item/completed","params":{"threadId":"another-thread","turnId":"fixture-turn","item":{"type":"agentMessage","text":"Unrelated"}}}
{"method":"item/completed","params":{"threadId":"fixture-thread","turnId":"fixture-turn","item":{"type":"agentMessage","text":"What should I do with this screenshot?","phase":"final_answer"}}}
{"method":"turn/completed","params":{"threadId":"fixture-thread","turn":{"id":"fixture-turn","status":"completed"}}}
`)
	var reply string
	if err := runCodexTurn(io.Discard, replies, "", nil, chiefInstructions, &reply); err != nil {
		t.Fatal(err)
	}
	if reply != "What should I do with this screenshot?" {
		t.Fatalf("unexpected reply %q", reply)
	}
}
