package execution

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
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
	if err := runCodexTurn(io.Discard, replies, "", nil, chiefInstructions, &reply, nil); err != nil {
		t.Fatal(err)
	}
	if reply != "What should I do with this screenshot?" {
		t.Fatalf("unexpected reply %q", reply)
	}
}

func TestChiefStructuredReplyKeepsGoalAfterLongText(t *testing.T) {
	expected := store.ChiefAnswer{Reply: strings.Repeat("界", 8192), Goal: "Fix the fixture."}
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	var item agentMessage
	item.ThreadID, item.TurnID, item.Item.Text = "thread", "turn", string(raw)
	item.Item.Phase = "final_answer"
	reply := conversationReply([]agentMessage{item}, "thread", "turn", true)
	var decoded store.ChiefAnswer
	if err := json.Unmarshal([]byte(reply), &decoded); err != nil || decoded != expected {
		t.Fatalf("structured output was truncated or lost: %v", err)
	}
	if clipped := clipConversationText(decoded.Reply, conversationTextLimit); len(clipped) > conversationTextLimit || !utf8.ValidString(clipped) {
		t.Fatal("reply was not clipped on a UTF-8 boundary")
	}
}
