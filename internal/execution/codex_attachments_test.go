package execution

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nep-ai/difqo/internal/store"
)

func TestCodexTurnIncludesAttachments(t *testing.T) {
	replies := strings.NewReader(`{"id":1,"result":{}}
{"id":2,"result":{"account":{"type":"chatgpt"}}}
{"id":3,"result":{"thread":{"id":"fixture-thread"}}}
{"id":4,"result":{"turn":{"id":"fixture-turn"}}}
{"method":"turn/completed","params":{"threadId":"fixture-thread","turn":{"id":"fixture-turn","status":"completed"}}}
`)
	var sent bytes.Buffer
	err := runCodex(&sent, replies, "Check the screenshot and log.", []store.Attachment{
		{Name: "diagnostic.log", MIME: "text/plain", Data: []byte("fixture error")},
		{Name: "screenshot.png", MIME: "image/png", Data: []byte("fixture image")},
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := json.NewDecoder(&sent)
	for requests.More() {
		var request struct {
			Method string
			Params struct {
				Input []map[string]string
			}
		}
		if err := requests.Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "turn/start" {
			continue
		}
		input := request.Params.Input
		if len(input) != 4 || input[0]["text"] != "Check the screenshot and log." ||
			!strings.Contains(input[1]["text"], "\nfixture error") ||
			!strings.Contains(input[2]["text"], "screenshot.png") ||
			input[3]["type"] != "image" || input[3]["url"] != "data:image/png;base64,Zml4dHVyZSBpbWFnZQ==" {
			t.Fatalf("unexpected Codex inputs: %+v", input)
		}
		return
	}
	t.Fatal("did not send a Codex turn")
}
