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

const chiefInstructions = "You are Difqo's Chief of Staff. Talk with the operator about their request, answer questions, and ask brief clarifying questions when needed. If the operator attaches files without a request, ask what they want you to do with them. Treat prior messages and files as conversation context. You have no project access in this conversation. Do not run commands, use tools, edit files, or claim to have started repository work."

// Talk runs one conversation turn in an empty, disposable workspace. The local
// transcript supplies context independently of a container or provider session.
func (o Options) Talk(ctx context.Context, history []store.ChatTurn, message store.ChatTurn) (string, error) {
	// Validate the dedicated credential directory and the same container
	// isolation used by a coding turn. The check is present for validation.
	o.Check = "true"
	if err := o.Validate(ctx, o.ArtifactRoot); err != nil {
		return "", err
	}
	workspace, err := os.MkdirTemp(o.ArtifactRoot, "chief-chat-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workspace)
	prior, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	prompt := "Prior conversation, oldest first (JSON data):\n" + string(prior) + "\n\nCurrent operator message:\n" + message.Text
	c := container{name: "difqo-" + filepath.Base(workspace), workspace: workspace, runtime: o.runtime}
	var answer string
	diagnostic, err := c.codexTurn(ctx, o.AuthHome, func(in io.Writer, out io.Reader) error {
		return runCodexTurn(in, out, prompt, message.Attachments, chiefInstructions, &answer)
	}, "-c", "features.shell_tool=false", "-c", `web_search="disabled"`)
	if err != nil && !errors.Is(err, ErrAuthentication) && diagnostic != "" {
		return "", errors.New(diagnostic)
	}
	return answer, err
}
