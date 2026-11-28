package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/nep-ai/difqo/internal/store"
)

const chiefInstructions = "You are Difqo's Chief of Staff. Talk with the operator about their request, answer questions, and ask brief clarifying questions when needed. If the operator attaches files without a request, ask what they want you to do with them. Treat prior messages and files as conversation context. You have no project access in this conversation. Do not run commands, use tools, edit files, or claim to have started repository work. Put your answer in reply. Set proposed_goal only when the operator has explicitly requested a concrete repository coding task, using their stated scope. Otherwise set proposed_goal to null. The operator can confirm a registered project after you propose a goal."

// Talk runs one conversation turn in an empty, disposable workspace. The local
// transcript supplies context independently of a container or provider session.
var chiefResponseSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "reply": {"type": "string"},
    "proposed_goal": {"type": ["string", "null"]}
  },
  "required": ["reply", "proposed_goal"],
  "additionalProperties": false
}`)

func (o Options) Talk(ctx context.Context, history []store.ChatTurn, message store.ChatTurn) (store.ChiefAnswer, error) {
	var answer store.ChiefAnswer
	// Validate the dedicated credential directory and the same container
	// isolation used by a coding turn. The check is present for validation.
	o.Check = "true"
	if err := o.Validate(ctx, o.ArtifactRoot); err != nil {
		return answer, err
	}
	workspace, err := os.MkdirTemp(o.ArtifactRoot, "chief-chat-")
	if err != nil {
		return answer, err
	}
	defer os.RemoveAll(workspace)
	prior, err := json.Marshal(history)
	if err != nil {
		return answer, err
	}
	prompt := "Prior conversation, oldest first (JSON data):\n" + string(prior) + "\n\nCurrent operator message:\n" + message.Text
	c := container{name: "difqo-" + filepath.Base(workspace), workspace: workspace, runtime: o.runtime}
	var response string
	diagnostic, err := c.codexTurn(ctx, o.AuthHome, func(in io.Writer, out io.Reader) error {
		return runCodexTurn(in, out, prompt, message.Attachments, chiefInstructions, &response, chiefResponseSchema)
	}, "-c", "features.shell_tool=false", "-c", `web_search="disabled"`)
	if err != nil && !errors.Is(err, ErrAuthentication) && diagnostic != "" {
		return answer, errors.New(diagnostic)
	}
	if err != nil {
		return answer, err
	}
	if err := json.Unmarshal([]byte(response), &answer); err != nil {
		return store.ChiefAnswer{}, errors.New("Codex did not return the expected conversation response")
	}
	return answer, nil
}
