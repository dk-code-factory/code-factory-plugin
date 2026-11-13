# Synchronize saved GitHub observations without historical execution

## Goal

Synchronize saved GitHub observations without historical execution.

## Preconditions

The private owner connection is established. Application data starts clean in the session-state database; native Codex authentication and configuration remain outside application state. GitHub source text is task data, not new authority.

## Main flow

1. Establish initial PR and activity baselines without launching historical work.

2. Import current revisions and preserve independent read and discovery state.

3. Retain checkpoints across authentication, quota and network failures. Manual scans and background synchronization share a persistent rolling-hour allowance of 2,000 request attempts by default; an explicitly configured allowance takes precedence.

4. Associate current-head CI changes with all affected PRs.

5. Normalize comments, reviews, threads and lifecycle updates across webhook and polling sources.

## Alternatives and recovery

Read-only inspection does not launch a model turn. Surface validation or provider errors and retain saved work. Human questions, explicit interruptions and uncertain deliveries hold automatic dispatch. Resume against current retained history; never replay an uncertain send or an answered RPC blindly. Explicit cleanup preserves dirty worktrees. A busy conversation defers only its own ingestion; polling continues discovering independent changes in the same repository and other repositories, including their quiet deadlines. Deferred baseline observations stay historical, deferred webhooks remain durable without artificial failure backoff, and cancellation stops the poll. Changed webhook source commits a metadata-only inbox notification; redelivery and unchanged source do not create extra notifications.

The shared GitHub response cache retains at most 64 MiB of response bodies and
4,096 responses across credential scopes. A new response first retires expired
entries, then only the oldest validated entries needed to fit these limits.
Unrelated recent responses keep their conditional validators across restart.
In-memory entries retain the headers supplied at admission. Durable entries keep
only approved validator and pagination headers. Eviction inspects freshness
metadata without loading retained bodies or headers.
Replacement and eviction commit together; a failed write does not publish a new
memory response, change its retained headers or discard its eviction candidates.
HTTP readers reject responses above their 16 MiB limit before cache admission.
If an internal cache admission exceeds the 64 MiB body allowance, it retains
neither that value nor an older response at the same key, preserving other entries.

## Scenarios

- **S1** — Establish initial PR and activity baselines without launching historical work.
- **S2** — Import current revisions and preserve independent read and discovery state.
- **S3** — Retain checkpoints across authentication, quota and network failures.
- **S4** — Associate current-head CI changes with all affected PRs.
- **S5** — Normalize comments, reviews, threads and lifecycle updates across webhook and polling sources.

## Verification scope

The assertion references below establish their fixture scope. Native installed-host, real-provider and assistive-technology acceptance require separate dated observations. The retired pipeline editor, review artifacts, AI question repair and built-in issue-report ledger have no compatibility path.

<!-- flow-references -->
## Terminology and UI references

Glossary: [normalized-event](../glossary/README.md#normalized-event), [repository](../glossary/README.md#repository), [work-item](../glossary/README.md#work-item).
UI entities: [PAGE-INBOX](../ui/README.md#PAGE-INBOX), [W-ITEM-ROW](../ui/README.md#W-ITEM-ROW), [W-EVENT-BATCHES](../ui/README.md#W-EVENT-BATCHES).

## Flow diagram

```mermaid
flowchart TD
    N0["Synchronize saved GitHub observations without historical execution"]
    N1["Establish initial PR and activity baselines without launching historical work #40;S1#41;"]
    N2["Import current revisions and preserve independent read and discovery state #40;S2#41;"]
    N3["Retain checkpoints across authentication, quota and network failures #40;S3#41;"]
    N4["Associate current-head CI changes with all affected PRs #40;S4#41;"]
    N5["Normalize comments, reviews, threads and lifecycle updates across webhook and polling sources #40;S5#41;"]
    N6(["Hold saved work and show the failure or human request"])
    N7(["Retain conversation and saved evidence"])
    N0 -->|"authorized action"| N1
    N1 -->|"validated or explicitly resumed"| N2
    N1 -->|"missing authority, stale identity or unavailable dependency"| N6
    N2 -->|"validated or explicitly resumed"| N3
    N2 -->|"missing authority, stale identity or unavailable dependency"| N6
    N3 -->|"validated or explicitly resumed"| N4
    N3 -->|"missing authority, stale identity or unavailable dependency"| N6
    N4 -->|"validated or explicitly resumed"| N5
    N4 -->|"missing authority, stale identity or unavailable dependency"| N6
    N5 -->|"validated or explicitly resumed"| N7
    N5 -->|"missing authority, stale identity or unavailable dependency"| N6
```

## Implementation references

- [internal/factory/github.go](../../internal/factory/github.go) — Durable webhook/poll normalization with initial baseline and CI association. (`func (s *Service) Poll(`)
- [internal/app/app.go](../../internal/app/app.go) — Retained revision-aware triage, priority precedence and relationships. (`func (a *App) UserAction(`)
- [internal/providers/pull_requests.go](../../internal/providers/pull_requests.go) — Complete current-head GraphQL review requests, lifecycle and thread observations. (`func (g *GitHub) EnrichPullRequest(`)
- [internal/transport/factory.go](../../internal/transport/factory.go) — Session, configuration, event and retained triage APIs. (`func NewFactory(`)
- [internal/providers/github_usage.go](../../internal/providers/github_usage.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/github_notifications.go](../../internal/providers/github_notifications.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/jira.go](../../internal/providers/jira.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/github_cache.go](../../internal/providers/github_cache.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/github_source.go](../../internal/providers/github_source.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/github_requests.go](../../internal/providers/github_requests.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/github_reviews.go](../../internal/providers/github_reviews.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/continuation.go](../../internal/providers/continuation.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/providers/github.go](../../internal/providers/github.go) — Current sync-inbox implementation or fixture/build dependency.
- [internal/store/store.go](../../internal/store/store.go) — Bodyless cache metadata and authoritative storage IDs are inventoried within the same transaction as eviction and replacement. (`func (t *Tx) JSONByteEntries(`)

## Test references

- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestBaselineAndPollingOverlap(`; S1, S2. Baseline observations launch no work; overlapping webhook/poll observations deduplicate.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestWebhookSignaturePersistenceRedelivery(`; S3, S5. Invalid HMAC is rejected, valid delivery persists before202, redelivery stays singular, and signed same-ID different body or event type returns503 without replacing the original ledger.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestCIWebhookAssociatesEveryCurrentHeadPRAndPollDeduplicates(`; S4. CI result refresh reaches every PR at the current SHA; overlapping poll emits no duplicate.
- [internal/providers/pull_requests_test.go](../../internal/providers/pull_requests_test.go) — `func TestPullRequestMetadataPaginationCurrentHeadAndRevision(`; S1. Current sync-inbox fixture assertion TestPullRequestMetadataPaginationCurrentHeadAndRevision. Inspect its exact assertion scope.
- [internal/providers/pull_requests_test.go](../../internal/providers/pull_requests_test.go) — `func TestPullRequestMetadataRateLimitBackoff(`; S1. Current sync-inbox fixture assertion TestPullRequestMetadataRateLimitBackoff. Inspect its exact assertion scope.
- [internal/providers/pull_requests_test.go](../../internal/providers/pull_requests_test.go) — `func TestUnprotectedPRReviewFallbackFollowsEveryOpinionPage(`; S1. Current sync-inbox fixture assertion TestUnprotectedPRReviewFallbackFollowsEveryOpinionPage. Inspect its exact assertion scope.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheAccountingPreservesLegacyEntriesAndReplacement(`; S1. Current sync-inbox fixture assertion TestGitHubPersistentCacheAccountingPreservesLegacyEntriesAndReplacement. Inspect its exact assertion scope.
- [internal/providers/github_reviews_test.go](../../internal/providers/github_reviews_test.go) — `func TestPublishedReviewsKeepVerdictCommitAndPaginatedInlineEvidence(`; S1. Current sync-inbox fixture assertion TestPublishedReviewsKeepVerdictCommitAndPaginatedInlineEvidence. Inspect its exact assertion scope.
- [internal/providers/github_reviews_test.go](../../internal/providers/github_reviews_test.go) — `func TestPublishedReviewCacheRetainsActualProviderObservationTime(`; S1. Current sync-inbox fixture assertion TestPublishedReviewCacheRetainsActualProviderObservationTime. Inspect its exact assertion scope.
- [internal/providers/github_cache_test.go](../../internal/providers/github_cache_test.go) — `func TestGitHubCacheExpiryInvalidationLiveWriteThroughAndRestart(`; S1. Current sync-inbox fixture assertion TestGitHubCacheExpiryInvalidationLiveWriteThroughAndRestart. Inspect its exact assertion scope.
- [internal/providers/github_cache_test.go](../../internal/providers/github_cache_test.go) — `func TestGitHubCacheLiveFailureNeverFallsBackAndHitsDoNotWaitForNetwork(`; S1. Current sync-inbox fixture assertion TestGitHubCacheLiveFailureNeverFallsBackAndHitsDoNotWaitForNetwork. Inspect its exact assertion scope.
- [internal/providers/github_cache_test.go](../../internal/providers/github_cache_test.go) — `func TestGitHubCacheDirectGraphReadUpdatesSharedHTTPState(`; S1. Current sync-inbox fixture assertion TestGitHubCacheDirectGraphReadUpdatesSharedHTTPState. Inspect its exact assertion scope.
- [internal/providers/github_cache_test.go](../../internal/providers/github_cache_test.go) — `func TestGitHubCacheNewHeadOrUpdateCannotReuseOldDetail(`; S1. Current sync-inbox fixture assertion TestGitHubCacheNewHeadOrUpdateCannotReuseOldDetail. Inspect its exact assertion scope.
- [internal/providers/github_cache_test.go](../../internal/providers/github_cache_test.go) — `func TestGitHubCachePreservesSummarySemanticsCopiesAndRelatedInvalidation(`; S1. Current sync-inbox fixture assertion TestGitHubCachePreservesSummarySemanticsCopiesAndRelatedInvalidation. Inspect its exact assertion scope.
- [internal/providers/work_queues_test.go](../../internal/providers/work_queues_test.go) — `func TestReviewRequestsRefreshWithoutIssueTimestampChange(`; S1. Current sync-inbox fixture assertion TestReviewRequestsRefreshWithoutIssueTimestampChange. Inspect its exact assertion scope.
- [internal/providers/work_queues_test.go](../../internal/providers/work_queues_test.go) — `func TestGitHubTeamMembershipPaginationAndFailure(`; S1. Current sync-inbox fixture assertion TestGitHubTeamMembershipPaginationAndFailure. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestManagedTransportsShareProcessGateAndPreserveCooldown(`; S1. Current sync-inbox fixture assertion TestManagedTransportsShareProcessGateAndPreserveCooldown. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestGitHubSharedBudgetSurvivesRestartSerializesAndFailsClosed(`; S1. Current sync-inbox fixture assertion TestGitHubSharedBudgetSurvivesRestartSerializesAndFailsClosed. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestGitHubPersistentConditionalCacheAuthIsolationAnd304RateObservation(`; S1. Current sync-inbox fixture assertion TestGitHubPersistentConditionalCacheAuthIsolationAnd304RateObservation. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestGitHubSecondaryCooldownBlocksRESTAndGraphQLAcrossRestart(`; S1. Current sync-inbox fixture assertion TestGitHubSecondaryCooldownBlocksRESTAndGraphQLAcrossRestart. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestIndexCallCountDoesNotGrowWithPRHistory(`; S1. Current sync-inbox fixture assertion TestIndexCallCountDoesNotGrowWithPRHistory. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestNotificationConditionalReadsHonorPollIntervalAcrossCursorChanges(`; S1. Current sync-inbox fixture assertion TestNotificationConditionalReadsHonorPollIntervalAcrossCursorChanges. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestGitHubRedirectsAreBudgetedAndNeverForwardAuthenticationOffOrigin(`; S1. Current sync-inbox fixture assertion TestGitHubRedirectsAreBudgetedAndNeverForwardAuthenticationOffOrigin. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestRateHeadersStopRequestsEvenWhenErrorBodyIsTruncated(`; S1. Current sync-inbox fixture assertion TestRateHeadersStopRequestsEvenWhenErrorBodyIsTruncated. Inspect its exact assertion scope.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestEmptyLocalCredentialCannotFallBackToAnonymousAPI(`; S1. Current sync-inbox fixture assertion TestEmptyLocalCredentialCannotFallBackToAnonymousAPI. Inspect its exact assertion scope.
- [internal/providers/github_requests_test.go](../../internal/providers/github_requests_test.go) — `func TestInboxRequestBudgetAndIndependentFreshness(`; S1. Current sync-inbox fixture assertion TestInboxRequestBudgetAndIndependentFreshness. Inspect its exact assertion scope.
- [internal/providers/github_requests_test.go](../../internal/providers/github_requests_test.go) — `func TestSharedCacheLRUAccountingAndExpiry(`; S1. Current sync-inbox fixture assertion TestSharedCacheLRUAccountingAndExpiry. Inspect its exact assertion scope.
- [internal/providers/github_requests_test.go](../../internal/providers/github_requests_test.go) — `func TestSharedOperationCacheAuthenticationAndSerialization(`; S1. Current sync-inbox fixture assertion TestSharedOperationCacheAuthenticationAndSerialization. Inspect its exact assertion scope.
- [internal/providers/github_requests_test.go](../../internal/providers/github_requests_test.go) — `func TestSharedPersistedCooldownAndPagination(`; S1. Current sync-inbox fixture assertion TestSharedPersistedCooldownAndPagination. Inspect its exact assertion scope.
- [internal/providers/github_requests_test.go](../../internal/providers/github_requests_test.go) — `func TestCheckRunLinksCompletenessAndIncrementalExpiry(`; S1. Current sync-inbox fixture assertion TestCheckRunLinksCompletenessAndIncrementalExpiry. Inspect its exact assertion scope.
- [internal/providers/github_test.go](../../internal/providers/github_test.go) — `func TestGitHubPaginationPREnrichmentAndRecovery(`; S1. Current sync-inbox fixture assertion TestGitHubPaginationPREnrichmentAndRecovery. Inspect its exact assertion scope.
- [internal/providers/github_test.go](../../internal/providers/github_test.go) — `func TestGitHubRateLimitsRevocationAndPaginationOrigin(`; S1. Current sync-inbox fixture assertion TestGitHubRateLimitsRevocationAndPaginationOrigin. Inspect its exact assertion scope.
- [internal/providers/github_test.go](../../internal/providers/github_test.go) — `func TestConditionalCacheKeepsPaginationAndScopesAuthentication(`; S1. Current sync-inbox fixture assertion TestConditionalCacheKeepsPaginationAndScopesAuthentication. Inspect its exact assertion scope.
- [internal/providers/github_test.go](../../internal/providers/github_test.go) — `func TestSyncEnrichmentSerializesRequestsAndCancelsOnFailure(`; S1. Current sync-inbox fixture assertion TestSyncEnrichmentSerializesRequestsAndCancelsOnFailure. Inspect its exact assertion scope.
- [internal/providers/continuation_test.go](../../internal/providers/continuation_test.go) — `func TestCurrentWorkItemRevalidatesCachedLifecycleAndFailsClosed(`; S1. Current sync-inbox fixture assertion TestCurrentWorkItemRevalidatesCachedLifecycleAndFailsClosed. Inspect its exact assertion scope.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestInitialDetailedImportBaselinesHistoryAndLaterThreadChanges(`; S1, S5. Actual Poll baselines detailed historical comments/threads and deduplicates a later resolved thread.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestSharedGitHubCooldownPreservesInboxCheckpoints(`; S3. Budgeted source synchronization preserves exact checkpoints during shared GitHub cooldown.
- [internal/app/app_test.go](../../internal/app/app_test.go) — `func TestScheduledSyncRecoversCredentialsAndFindsNewItems(`; S2, S3. Scheduled sync recovers credentials and observes newly discovered source items without losing baseline state.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestDiscussionWebhooksKeepNewerPRContext(`; S2. Late review, inline-comment and thread webhook snapshots retain newer saved PR head, title and labels in both saved state and normalized event context.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestCommentDeletionNormalizesOnceAcrossWebhookAndPolling(`; S5. Discussion and inline-comment deletion normalize once through both webhook and poll despite unchanged body/timestamp and redelivery.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestDetailRefreshAdvancesPastSkippedAndFailedItems(`; S2. Detail cursor advances past paused, removed and failed rows to refresh the later eligible item while preserving the failure.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestInitialDetailBaselineCannotLaunchUnprocessedHistory(`; S1. Multiple initial detail pages establish historical comments as baseline without starting sessions.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestPollReadsOnlyDuePendingDeliveries(`; S5. Polling selects due unprocessed webhook rows without decoding processed archives or future pending deliveries.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestCIRefreshPreservesDiscussionWhenProviderReturnsEmptyActivity(`; S4. Four CI webhook types preserve saved discussion when provider activity is empty; queued events contain compact context and the delivered batch contains full current discussion once.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestPollFirstWebhookEnrichesActionsWithoutDuplicateRules(`; S5. Poll-first request/comment-created/comment-edited observations are enriched by later webhook actions once, with one canonical event, ordered specific rules and no repeated general rule before or after prior delivery.
- [internal/store/factory_reads_test.go](../../internal/store/factory_reads_test.go) — `func TestDueRecordsCompareInstantsAcrossTimezones(`; S3. Due-record selection includes a past positive-offset timestamp and excludes a future negative-offset timestamp by comparing instants.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestWebhookLifecycleReturningToBaselineStillRunsOnce(`; S1, S5. A received reopened webhook matching an initial open baseline still creates one authorized action; redelivery and overlapping polling do not repeat it.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestReviewThreadWebhooksUseActionWithoutInventedResolvedField(`; S5. Resolved and unresolved webhooks use top-level action and node_id without invented is_resolved; saved state changes and each transition queues once across redelivery/polling.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestReviewStateCasingDeduplicatesActualRESTAndWebhookOverlap(`; S5. Actual REST uppercase review state and lowercase webhook normalize identically in either order, producing one action without resetting the quiet deadline.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestCachedDetailCannotReverseNewerWebhookObservation(`; S2, S5. A cached older detail snapshot cannot reverse a newer resolved webhook or queue a false reverse action; a genuinely later unresolved snapshot remains observable.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestCommentCatchupStoresLinearDeltasAndWritesQueueOnce(`; S5. A catch-up of 100 comments at 4 KiB each performs one SQL session write and keeps aggregate events and the queued session each within 1 MiB; a selected comment marker occurs exactly twice in the delivered batch and actual native input.
- [internal/factory/regressions_test.go](../../internal/factory/regressions_test.go) — `func TestDelayedCommentWebhookCannotReverseNewerActivity(`; S5. Older timezone-aware discussion/inline-comment webhook versions cannot replace retained B or cause poll duplication; genuinely newer C is saved and authorizes one action.
- [internal/factory/review7_test.go](../../internal/factory/review7_test.go) — `func TestSignedWebhookCommitsInboxNotificationWithoutProviderRead(`; S2, S5. A signed webhook persists changed PR source and emits one metadata-only inbox.changed subscription for matching and absent rules, without private body/native turn. Redelivery and same-source new delivery emit no extra notification.
- [internal/factory/review7_test.go](../../internal/factory/review7_test.go) — `func TestPollingDiscoversPeerChangesWhileNativeAttachmentIsHeld(`; S1, S2, S5. Five held-attachment modes cover unchanged issue, changed PR, detail refresh, initial baseline and busy webhook. Actual Poll discovers same- and other-repository peer heads and sets their 120-second quiet deadlines before the held attachment releases. Busy webhook remains pending without backoff; canceled Poll stops; release+two polls preserve one deferred head transition/no initial historical queue and no extra native turns.
- [internal/providers/github_transport_cache_test.go](../../internal/providers/github_transport_cache_test.go) — `func TestGitHubRESTTransportsSharePersistentCacheAndLiveReads(`; S3. Typed and generic REST share persisted bodies and ETags across restart; forced live reads update both paths, expiry revalidates, copies remain isolated and another credential cannot reuse the result.
- [internal/providers/github_transport_cache_test.go](../../internal/providers/github_transport_cache_test.go) — `func TestGitHubGenericCachePersistsLastModifiedAndNeverMemoizesWrites(`; S3. Restart retains Last-Modified, response body and pagination; 200/304 durable records exclude cookies and private headers while live responses preserve them. Repeated writes dispatch independently while batch GETs memoize and forced live reads bypass the memo.
- [internal/providers/github_transport_cache_test.go](../../internal/providers/github_transport_cache_test.go) — `func TestGitHubGraphCacheSeparatesOriginsAndSurvivesRestart(`; S3. Identical GraphQL queries and credentials at two API origins retain independent results across client restarts.
- [internal/providers/github_transport_cache_test.go](../../internal/providers/github_transport_cache_test.go) — `func TestGitHubRevocationInvalidatesCredentialCacheAcrossTransportsAndRestart(`; S3. A live 401/403 with empty, truncated or oversized body, or a GraphQL FORBIDDEN/UNAUTHORIZED envelope, invalidates the same credential response/query caches across operations and restart in memory/durable modes; another credential retains hits. Mixed auth/rate envelopes in either order, using type and extensions.code, retain rate classification while invalidating; auth with zero remaining quota also invalidates.
- [internal/providers/github_transport_cache_test.go](../../internal/providers/github_transport_cache_test.go) — `func TestGitHubCacheGenerationFailuresCannotReturnCachedSuccess(`; S3. A real SQLite trigger rejecting generation writes blocks subsequent same-client response/query cache hits; malformed persisted generation data fails closed after client restart without contacting GitHub. Mixed GraphQL auth/rate generation-write failure returns cache failure before rate classification and blocks that client query hits.
- [internal/app/github_usage_test.go](../../internal/app/github_usage_test.go) — `func TestGitHubDefaultBudgetAndExplicitOverride(`; S3. Application policy defaults to 2,000 request attempts per hour while retaining an explicit 600-request override.
- [internal/providers/github_usage_test.go](../../internal/providers/github_usage_test.go) — `func TestGitHubDefaultBudgetReportingAndAdmission(`; S3. Default and saved usage report 2,000, preserve explicit overrides and prior attempts, admit request 2,000, and defer request 2,001 until the rolling window frees capacity.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCachePressureRetainsHotConditionalResponses(`; S1, S3. Real 64 MiB pressure evicts one oldest response, retains hot memory data and preserves a conditional 304 read after restart.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheEvictsExpiredMemoryAndKeepsReplacement(`; S1, S3. Age-expired and endpoint-expired rows leave durable and memory caches while a replacement stays identical after restart.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheRejectedWriteDoesNotPublishOrEvictMemory(`; S3. An SQLite rejection rolls back prior expiry deletions and does not publish a fresh response or remove the old memory copy.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheRejectedRevalidationPreservesMemoryHeaders(`; S3. A managed live304 with changed pagination headers and a rejected cache INSERT leaves body, headers and freshness identical in memory and after restart; the next ordinary read stays local.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheKeepsLiveHeadersInMemoryAndFiltersDurableCopy(`; S3. A generic managed GET and its local hit preserve full live headers with one request, while a restarted provider reuses only approved durable headers without another request.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheOversizedReplacementPreservesOtherEntries(`; S3. An oversized replacement is not retained in memory or durable state, removes its old same-key response and preserves unrelated entries.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheEntryPressureEvictsOneOldestRow(`; S1, S3. The 4,096-entry bound evicts only one oldest row with deterministic storage-ID tie breaking.
- [internal/store/store_test.go](../../internal/store/store_test.go) — `func TestJSONByteEntriesKeepBodylessMetadataAndTransactionalIdentity(`; S1, S3. Transactional inventory selects only requested metadata fields and decoded byte sizes, keeping large headers and other unselected payloads inside SQLite; source rows, storage identities, bound SQL inputs and cursor closure remain intact.
- [internal/providers/github_cache_bounds_test.go](../../internal/providers/github_cache_bounds_test.go) — `func TestGitHubPersistentCacheEvictionKeepsLargeHeadersAfterRestart(`; S1, S3. Eviction reads scalar freshness fields, retires an old large-header response and preserves an unrelated approved Link header and body across restart.

## Verification and gaps

Local suites: `go`, `browser`.
Fixture success does not establish real-host acceptance.

<!-- /flow-references -->
