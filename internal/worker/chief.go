package worker

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/nep-ai/difqo/internal/execution"
	"github.com/nep-ai/difqo/internal/store"
)

// RunChiefQueue starts at most one project coding worker at a time. Jobs
// survive service restarts and can only be claimed by their registered binding.
func RunChiefQueue(ctx context.Context, tasks store.Tasks, projects func() []store.SpecialistProject, options execution.Options, validate func(store.SpecialistProject) bool) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		for _, project := range projects() {
			if ctx.Err() != nil {
				return
			}
			if project.ExecutionBinding == "" {
				continue
			}
			if err := runChiefProject(ctx, tasks, project, options, validate); err != nil && ctx.Err() == nil {
				log.Printf("Chief of Staff project worker could not finish for project %q (%T)", project.ID, err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runChiefProject(ctx context.Context, tasks store.Tasks, project store.SpecialistProject, options execution.Options, validate func(store.SpecialistProject) bool) error {
	queued, err := tasks.HasQueuedChiefProject(ctx, project)
	if err != nil || !queued || !validate(project) {
		return err
	}
	options.Check = project.Check
	if err := options.Validate(ctx, project.Checkout); err != nil {
		// Leave the job available when Docker or the credentials are not ready.
		return err
	}
	task, err := tasks.ClaimChiefProject(ctx, project)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	id := task.Attempt.ID
	result, err := runCodexTask(ctx, project.Checkout, task, options, func(ctx context.Context) error {
		return tasks.Renew(ctx, id)
	})
	if err != nil {
		return err
	}
	return tasks.Complete(ctx, id, result)
}
