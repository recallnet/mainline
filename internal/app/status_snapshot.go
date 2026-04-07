package app

import (
	"context"
	"path/filepath"

	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type repoStatusSnapshot struct {
	ExecutionEstimate         executionEstimate
	Counts                    queueCounts
	QueueSummary              queueSummary
	UnfinishedQueueItems      []string
	Alerts                    []string
	LatestSubmission          *statusSubmission
	LatestPublish             *statusPublish
	ActiveSubmissions         []statusSubmission
	ActivePublishes           []statusPublish
	IntegrationWorker         *state.LeaseMetadata
	PublishWorker             *state.LeaseMetadata
	ProtectedWorktreeActivity *protectedWorktreeActivity
	RecentEvents              []state.EventRecord
}

func loadRepoStatusSnapshot(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, limit int) (repoStatusSnapshot, error) {
	submissions, err := store.ListIntegrationSubmissions(ctx, repoRecord.ID)
	if err != nil {
		return repoStatusSnapshot{}, err
	}
	requests, err := store.ListPublishRequests(ctx, repoRecord.ID)
	if err != nil {
		return repoStatusSnapshot{}, err
	}
	events, err := store.ListEvents(ctx, repoRecord.ID, limit)
	if err != nil {
		return repoStatusSnapshot{}, err
	}

	enrichedSubmissions, err := enrichStatusSubmissions(ctx, store, repoRecord.ID, cfg.Repo.MainWorktree, cfg.Repo.ProtectedBranch, submissions)
	if err != nil {
		return repoStatusSnapshot{}, err
	}
	estimate, err := collectExecutionEstimate(ctx, store, repoRecord.ID, cfg, submissions)
	if err != nil {
		return repoStatusSnapshot{}, err
	}
	enrichedSubmissions = annotateQueueEstimates(enrichedSubmissions, estimate)
	queue, err := loadQueueSnapshot(store, repoRecord.ID)
	if err != nil {
		return repoStatusSnapshot{}, err
	}

	snapshot := repoStatusSnapshot{
		ExecutionEstimate:    estimate,
		Counts:               queue.Counts,
		QueueSummary:         queue.Summary,
		UnfinishedQueueItems: queue.UnfinishedItems,
		Alerts:               buildStatusAlerts(queue.Counts),
		ActiveSubmissions:    activeSubmissions(enrichedSubmissions),
		ActivePublishes:      activePublishes(requests),
		RecentEvents:         events,
	}

	lockManager := state.NewLockManager(repoRecord.CanonicalPath, stateGitDirFromStorePath(store.Path))
	if metadata, ok := readActiveLease(lockManager, state.IntegrationLock); ok {
		snapshot.IntegrationWorker = &metadata
	}
	if metadata, ok := readActiveLease(lockManager, state.PublishLock); ok {
		snapshot.PublishWorker = &metadata
	}
	snapshot.ProtectedWorktreeActivity = buildProtectedWorktreeActivity(cfg.Repo.MainWorktree, snapshot.IntegrationWorker, snapshot.PublishWorker)

	if len(enrichedSubmissions) > 0 {
		latest := enrichedSubmissions[len(enrichedSubmissions)-1]
		snapshot.LatestSubmission = &latest
	}
	if len(requests) > 0 {
		latest := newStatusPublish(requests[len(requests)-1])
		if snapshot.PublishWorker != nil && snapshot.PublishWorker.RequestID == latest.ID {
			latest.ActiveStage = snapshot.PublishWorker.Stage
		}
		snapshot.LatestPublish = &latest
	}
	if snapshot.PublishWorker != nil {
		for i := range snapshot.ActivePublishes {
			if snapshot.ActivePublishes[i].ID == snapshot.PublishWorker.RequestID {
				snapshot.ActivePublishes[i].ActiveStage = snapshot.PublishWorker.Stage
			}
		}
	}

	return snapshot, nil
}

func stateGitDirFromStorePath(path string) string {
	// state.DefaultPath writes under <gitdir>/mainline/state.db; derive the gitdir back from that stable layout.
	return filepath.Dir(filepath.Dir(path))
}
