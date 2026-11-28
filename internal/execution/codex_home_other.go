//go:build !darwin && !linux

package execution

import (
	"context"
	"errors"
)

func lockCodexHome(_ context.Context, home string) (func(), error) {
	if home != "" {
		return nil, errors.New("local Codex authentication requires macOS or Linux")
	}
	return func() {}, nil
}
