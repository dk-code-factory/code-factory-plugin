package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"codefactory/internal/store"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRetainedReviewContinuationHonorsPluginMaintenance(t *testing.T) {
	for _, operation := range []string{"continue", "resume"} {
		for _, adapter := range []string{"fixture", "unavailable"} {
			t.Run(operation+"/"+adapter, func(t *testing.T) {
				a, item, workers, collector := retainedFixture(t)
				ctx := a.Runs.ctx
				workers.second = make(chan struct{})
				workers.release = make(chan struct{})
				release := sync.OnceFunc(func() { close(workers.release) })
				t.Cleanup(func() {
					release()
					waitLaunchRetry(t, a.Runs.Close)
				})
				run, err := a.Runs.PreparePRReview(ctx, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Runs.Start(ctx, run.ID, false); err != nil {
					t.Fatal(err)
				}
				waitRetainedReviewValue(t, workers.second, "reviewer before interruption")
				if err = a.Runs.Interrupt(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
				waitGuardReviewDone(t, a, run.ID)
				if err = a.Store.Get(ctx, "run", run.ID, &run); err != nil {
					t.Fatal(err)
				}
				var retained retainedReview
				if err = a.Store.Get(ctx, "review-workflow", run.ID, &retained); err != nil {
					t.Fatal(err)
				}
				prepareCalls, reviewCalls, captures := workers.prepareCalls, workers.reviewCalls, collector.captures
				if adapter == "unavailable" {
					a.ReviewEngine = unavailableReviewEngine{}
				}
				assertUnchanged := func() {
					t.Helper()
					var saved domain.Run
					var session retainedReview
					if err := a.Store.Get(ctx, "run", run.ID, &saved); err != nil {
						t.Fatal(err)
					}
					if err := a.Store.Get(ctx, "review-workflow", run.ID, &session); err != nil {
						t.Fatal(err)
					}
					delivery, err := a.MessageDelivery(ctx, run.ID)
					messages, listErr := store.List[MessageDelivery](ctx, a.Store, "message")
					if err != nil || listErr != nil || delivery != nil || len(messages) != 0 || !reflect.DeepEqual(saved, run) || !reflect.DeepEqual(session, retained) {
						t.Fatalf("refused continuation mutated retained state or accepted a message: run=%+v delivery=%+v errors=%v/%v", saved, delivery, err, listErr)
					}
					if workers.prepareCalls != prepareCalls || workers.reviewCalls != reviewCalls || collector.captures != captures {
						t.Fatal("refused continuation started collection, preparation or reviewer work")
					}
				}
				// PluginAction and dependency maintenance hold this same exclusive
				// gate. Hold it directly to avoid a timing-dependent slow provider.
				a.Runs.adminGate.Lock()
				unlock := sync.OnceFunc(a.Runs.adminGate.Unlock)
				t.Cleanup(unlock)
				if _, err = a.Runs.QueueMessage(ctx, run.ID, operation, ""); err == nil || !strings.Contains(err.Error(), "plugin maintenance") {
					t.Fatal("retained continuation bypassed maintenance refusal", err)
				}
				assertUnchanged()
				unlock()
				if adapter == "unavailable" {
					if _, err = a.Runs.QueueMessage(ctx, run.ID, operation, ""); err == nil || !strings.Contains(err.Error(), prreview.OrderedIteration) {
						t.Fatal("released maintenance lost production capability rejection", err)
					}
					assertUnchanged()
					a.ReviewEngine = nil
				}
				if _, err = a.Runs.QueueMessage(ctx, run.ID, operation, "stale-turn"); err == nil {
					t.Fatal("retained continuation accepted stale turn authority")
				}
				assertUnchanged()
				release()
				delivery, err := a.Runs.QueueMessage(ctx, run.ID, operation, "")
				if err != nil || delivery.State != "delivered" {
					t.Fatal("released maintenance did not allow retained continuation", delivery, err)
				}
				waitGuardReviewDone(t, a, run.ID)
				if err = a.Store.Get(ctx, "run", run.ID, &run); err != nil || run.Status != "idle" || !run.ReviewComplete || run.ThreadID != "" {
					t.Fatal("retained continuation did not complete without native work", run, err)
				}
				if workers.prepareCalls != prepareCalls || workers.reviewCalls != reviewCalls+1 || collector.captures != captures {
					t.Fatal("continuation failed to reuse its captured evidence and completed reviewer")
				}
			})
		}
	}
}
