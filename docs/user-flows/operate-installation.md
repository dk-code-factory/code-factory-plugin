# Inspect health, upgrade, roll back and uninstall

## Goal

The owner can diagnose the installation and change releases while preserving fresh wrapper application state and existing connection credentials and unrelated computer services.

## Preconditions

The owner has a local installation and, for a release change, a validated package.

## Main flow

1. Inspect health and distinguish verified, failed and unverified checks.
2. Upgrade to a validated release after preserving the active factory-v1.db snapshot or recording its prior absence.
3. If necessary, explicitly roll back using the retained compatible snapshot.
4. Uninstall application services and launchers while retaining application data and connection mapping.
5. Configure native profiles and agents through confirmed configuration proposals; active turns finish before changes apply.

## Alternatives and recovery

Invalid packages or occupied ports fail without replacing a healthy installation. Failed upgrades attempt automatic recovery. If interruption occurs after a release is activated but before the change is finalized, explicit rollback can restore the retained compatible snapshot. Rollback preserves newer state in an archive. Shared network routes and login policy are preserved. Browser inspection uses an explicitly owner-approved attachment mode. A dedicated persistent pilot profile requires its own initial manual sign-in without copying authentication; inspection pauses during that sign-in. Local debugger readiness alone does not establish application or host acceptance. Upgrading changed embedded UI content also changes its advertised resource identity; hosts may still require a metadata refresh or fresh view. Verify the actual rendered revision rather than inferring it from healthy service or local resource reads. Operator evidence is version-bound and never inferred from fixture success.

The first legacy-to-wrapper cutover preserves the legacy database without importing it. Recovery restores the prior absence of wrapper state before starting the previous binary. Explicit rollback archives newly created wrapper state before restoring that absence. A missing recorded database snapshot fails before stopping services.

The owner can inspect private rotating diagnostics for incoming MCP and owner API calls, with UTC start time, API name, bounded safe parameters, transport status and server processing duration. The duration does not measure client latency or background agent completion. Completed responses need not wait for diagnostic storage. Records retain the committed transport status when available; an uncommitted panic records an explicit unknown transport status separately from its handler failure. Records can arrive after the response or out of request order. Logs survive restart, rotate within fixed bounds and never broaden request authority. Calls rejected before work starts are recorded without reading their bodies. If rejection diagnostics saturate, marked aggregate records retain exact rejection counts and UTC time intervals; individual API identities and parameters for that overflow are unavailable. Unsafe paths and write failures produce a generic warning while preserving API behavior. Existing earlier calls are not reconstructed.

Doctor bounds the complete diagnostics retrieval to 20 seconds, including polling and large-result retrieval. A continuously pending operation reports the timeout without a second submission.

## Scenarios

- **S1** Fresh/rerun installation and failed upgrade preserve private state. Application and task commands use the recorded provider programs without replacing global programs; prior provider command selections remain available for compatible rollback. Explicit rollback recovers an interruption after release activation but before the change is finalized, restoring the compatible snapshot and archiving newer state.
- **S2** Rollback restores compatible state and transport profile while archiving newer state.
- **S3** Uninstall/reinstall retain data and mapping; shared routes and unrelated services are preserved.
- **S4** Diagnostics expose version-bound verified/failed/unverified evidence, including outstanding host acceptance.
- **S5** Configuration edits require an explicit confirmed diff, stale versions fail, and active work reaches a safe boundary before application. Legacy pipeline, review and skill-migration APIs are retired.

<!-- flow-references -->
## Terminology and UI references

Glossary: [owner](../glossary/README.md#owner), [verification](../glossary/README.md#verification), [coverage-gap](../glossary/README.md#coverage-gap), [unverified-behavior](../glossary/README.md#unverified-behavior), [dependency-operation](../glossary/README.md#dependency-operation), [github-skill-source](../glossary/README.md#github-skill-source).
UI entities: [PAGE-BROWSER-LAB](../ui/README.md#PAGE-BROWSER-LAB), [P-BROWSER-LAB](../ui/README.md#P-BROWSER-LAB), [W-BROWSER-LAB](../ui/README.md#W-BROWSER-LAB), [C-BROWSER-LAB](../ui/README.md#C-BROWSER-LAB).

## Flow diagram

```mermaid
flowchart TD
    N0["Inspect version-bound verified, failed and unverified checks #40;S4#41;"]
    N1["Upgrade validated release with retained compatible state #40;S1#41;"]
    N2["Attempt automatic recovery after returned upgrade failure"]
    N3["Explicitly roll back and archive newer state #40;S2#41;"]
    N4["Uninstall while retaining data and mapping #40;S3#41;"]
    N5(["Preserve unrelated services and shared routes"])
    N6["Reject invalid package or occupied port"]
    N7["Release activated but interrupted before change is finalized"]
    N8["Authenticate owner and inspect or update identified managed dependencies #40;S5#41;"]
    N9(["Observe verified per-dependency outcomes#59; retain backups and existing authentication"])
    N10(["Reject busy, invalid or incompatible changes#59; retain state and inspect uncertain outcomes"])
    N11["Record safe request diagnostics or explicit coalesced rejection counts #40;S4#41;"]
    N12(["Retain private bounded logs with secret values redacted"])
    N13(["Warn once if logging is unavailable#59; preserve the API result without replay"])
    N14(["Retain running services and report unavailable recorded snapshot"])
    N0 -->|"upgrade"| N1
    N1 -->|"upgrade returns failure"| N2
    N2 -->|"recovered"| N0
    N0 -->|"rollback"| N3
    N3 --> N0
    N0 -->|"uninstall"| N4
    N4 --> N5
    N1 -->|"success"| N5
    N1 -->|"invalid or conflicting"| N6
    N6 -->|"healthy installation retained"| N0
    N1 -->|"interrupted after activation before finalization"| N7
    N7 -->|"explicit rollback"| N3
    N2 -->|"automatic recovery fails#59; explicit rollback"| N3
    N0 -->|"explicit owner dependency maintenance"| N8
    N8 -->|"authorized idle operation and verified changes"| N9
    N8 -->|"unauthorized, active work, edits after baseline, failed probe or interruption"| N10
    N0 -->|"incoming MCP or private owner API call"| N11
    N11 -->|"safe bounded record appended#59; rotate older entries"| N12
    N11 -->|"unsafe path or log write failure"| N13
    N3 -->|"recorded database or transport snapshot unavailable"| N14
```

## Implementation references

- [internal/setup/lifecycle.go](../../internal/setup/lifecycle.go) — Install, snapshots, deferred automatic recovery from returned upgrade errors, explicit rollback using retained pending.json operation state, and uninstall.
- [internal/setup/tailscale.go](../../internal/setup/tailscale.go) — Scoped transport configuration
- [internal/setup/tailscale_routes.go](../../internal/setup/tailscale_routes.go) — Route ownership and conflict checks
- [internal/setup/diagnostics.go](../../internal/setup/diagnostics.go) — Shared typed health diagnostics
- [internal/setup/checkpoints.go](../../internal/setup/checkpoints.go) — Operator setup checkpoints
- [internal/setup/evidence.go](../../internal/setup/evidence.go) — Version-bound acceptance evidence
- [scripts/package.py](../../scripts/package.py) — Content-addressed release archives
- [internal/domain/types.go](../../internal/domain/types.go) — Shared domain, persistence, transport or dependency contract
- [internal/store/store.go](../../internal/store/store.go) — Shared domain, persistence, transport or dependency contract
- [internal/transport/mcp.go](../../internal/transport/mcp.go) — Shared domain, persistence, transport or dependency contract
- [go.mod](../../go.mod) — Shared domain, persistence, transport or dependency contract
- [go.sum](../../go.sum) — Shared domain, persistence, transport or dependency contract
- [scripts/Containerfile.systemd](../../scripts/Containerfile.systemd) — Supporting inspect health, upgrade, roll back and uninstall
- [scripts/browser_control.mjs](../../scripts/browser_control.mjs) — Local stdio controller chooses approved native/pilot/explicit loopback attachment without launching Chrome, copying profiles or exporting authentication. (`const options = parseBrowserOptions(`)
- [scripts/browser_lab.mjs](../../scripts/browser_lab.mjs) — Supporting inspect health, upgrade, roll back and uninstall
- [scripts/connection_probe.py](../../scripts/connection_probe.py) — Supporting inspect health, upgrade, roll back and uninstall
- [scripts/smoke.py](../../scripts/smoke.py) — Supporting inspect health, upgrade, roll back and uninstall
- [internal/buildinfo/version.go](../../internal/buildinfo/version.go) — Supporting inspect health, upgrade, roll back and uninstall
- [internal/testutil/testutil.go](../../internal/testutil/testutil.go) — Shared fake-provider verification input; changes require assertion reconciliation.
- [internal/testutil/fakecodex/main.go](../../internal/testutil/fakecodex/main.go) — Shared fake-provider verification input; changes require assertion reconciliation.
- [scripts/browser_options.mjs](../../scripts/browser_options.mjs) — Pure explicit attachment options retain privacy flags, reject unsafe/ambiguous endpoints and never echo rejected credential-bearing URL values. (`export function parseBrowserOptions(`)
- [internal/transport/mcp.go](../../internal/transport/mcp.go) — Advertises the embedded UI under a full-content SHA256 URI and binds all tool/output-template references to it, rejecting unregistered and retired resource URIs. (`func registerUIResource(`)
- [internal/gateway/proxy_diagnostics.go](../../internal/gateway/proxy_diagnostics.go) — Fixed safe upstream failure classes, preserving request secrecy and HTTP behavior. (`func proxyError(`)
- [internal/commandenv/providers.go](../../internal/commandenv/providers.go) — Generate private versioned command launchers for exactly the recorded provider executables, quote paths literally and preserve prior launcher sets for rollback. (`func Prepare(`)
- [internal/codex/client.go](../../internal/codex/client.go) — No-model compatibility probe covering initialize, current account, skills, config and native plugin inventory. (`func Probe(`)
- [docs/dependency-admin.md](../../docs/dependency-admin.md) — Owner dependency API request, recovery, provenance and supported dependency boundaries.
- [internal/transport/api_log.go](../../internal/transport/api_log.go) — Record incoming MCP/owner API call names, safe parameters, UTC start time, HTTP status and handler duration in a private bounded rotating JSONL log without changing request processing. (`func (s *Server) LogAPI(`)
- [internal/transport/api_log_parameters.go](../../internal/transport/api_log_parameters.go) — Redact credentials, answer/free-text payloads and metadata; mask URL userinfo/query/fragment and bound remaining parameters before persistence. (`func privateAPIParameter(`)
- [scripts/smoke.py](../../scripts/smoke.py) — Compiled fresh-state session daemon smoke checks.
- [internal/factory/configuration.go](../../internal/factory/configuration.go) — Explicit confirmed app/native configuration at safe boundaries. (`func (s *Service) Confirm(`)
- [internal/httpbudget/budget.go](../../internal/httpbudget/budget.go) — Finite response deadlines, bounded admission/body buffer and explicit response outcome; acknowledged SSE retains its cancellation lifetime. (`func Wrap(`)
- [internal/httpbudget/budget.go](../../internal/httpbudget/budget.go) — A bounded diagnostic lane retains ordinary admission rejections or explicit coalesced overflow counts without starting business work or reading private requests. (`func AuditRejections(`)

## Test references

- [scripts/test_installer.py](../../scripts/test_installer.py) — `def test_install_rerun_paths_backup_failed_upgrade_rollback_uninstall(`; S1, S2, S3. Packaged process-backed install/rerun, returned upgrade failure with deferred automatic recovery, explicit rollback, successful upgrade and uninstall/reinstall. It does not interrupt the installer process or establish recovery from a durable pending operation left by interruption.
- [scripts/test_installer.py](../../scripts/test_installer.py) — `def test_interrupted_upgrade_pending_state_restores_database_and_archives_new_state(`; S1. Process-backed fixture hard-kills the upgrade after release activation at the service-restart gate, before installation.json commit, with pending.json and a compatible backup retained. Fresh rollback must select pending state because the committed installation has no previous release; asserts old release health, restoration of the pre-upgrade database sentinel, archiving of a post-kill mutation, unchanged owner token and removal of pending state. Covers this single interruption boundary only.
- [internal/setup/gateway_profile_backup_test.go](../../internal/setup/gateway_profile_backup_test.go) — `func TestRollbackRestoresGatewayProfileAndArchivesCurrent(`; S2. Gateway profile snapshot, restore and archive.
- [internal/setup/tailscale_prefix_test.go](../../internal/setup/tailscale_prefix_test.go) — `func TestConfigureTailscaleMigratesRerunsAndRemovesOnlyOwnedRoutes(`; S3. Scoped route migration and ownership.
- [internal/setup/checkpoints_test.go](../../internal/setup/checkpoints_test.go) — `func TestOwnershipRequiredAndVersionBoundEvidence(`; S4. Ownership checkpoint and version-bound evidence.
- [internal/setup/lifecycle_test.go](../../internal/setup/lifecycle_test.go) — `func TestSnapshotIncludesCommittedWALAndPreservesSource(`; S1. Consistent WAL snapshot.
- [scripts/test_systemd.py](../../scripts/test_systemd.py) — `def run(`; S3. Opt-in isolated rootless systemd lifecycle; not run by default.
- [scripts/test_live_connection_test.py](../../scripts/test_live_connection_test.py) — `def test_private_dns_success_cannot_mask_public_tls_failure(`; S4. Offline probe regression; tailnet is not public ingress.
- [scripts/test_connection_probe.py](../../scripts/test_connection_probe.py) — `def test_discovery_and_non_authorization(`; S4. Discovery-only probe cannot grant authorization.
- [scripts/test_browser_lab.mjs](../../scripts/test_browser_lab.mjs) — `test('owner, Host, session, Origin and CSRF protect every browser action'`; S4. Private browser-controller fixture access boundaries.
- [scripts/test_mcp_sdk.mjs](../../scripts/test_mcp_sdk.mjs) — `async function sdkFetch(`; S4. Opt-in independent deployed OAuth/MCP test; not real host acceptance.
- [scripts/test_live_connection.py](../../scripts/test_live_connection.py) — `class ConnectionTest:`; S4. Opt-in deployed public TLS/OAuth/MCP probe; not run by reconciliation and not real host acceptance.
- [scripts/test_browser_control.mjs](../../scripts/test_browser_control.mjs) — `test('no arguments preserve owner-approved auto-connect and all privacy flags'`; S4. Default controller transport retains native auto-connect and every privacy flag. Pure argument fixtures do not attach Chrome, read profiles or establish host acceptance.
- [scripts/test_browser_control.mjs](../../scripts/test_browser_control.mjs) — `test('pilot mode attaches only to the dedicated existing loopback browser'`; S4. Explicit pilot configuration selects existing loopback9224 without launch/profile/cookie arguments. Pure argument fixtures do not attach Chrome, read profiles or establish host acceptance.
- [scripts/test_browser_control.mjs](../../scripts/test_browser_control.mjs) — `test('explicit endpoint accepts split and equals options and exact IPv4 or IPv6 loopback literals'`; S4. Explicit split/equals URL options accept only supported literal loopback endpoints and canonical ports. Pure argument fixtures do not attach Chrome, read profiles or establish host acceptance.
- [scripts/test_browser_control.mjs](../../scripts/test_browser_control.mjs) — `test('every supported mode retains statistics, CrUX and header protections exactly once'`; S4. Every supported option mode keeps statistics, CrUX and header-redaction protections exactly once. Pure argument fixtures do not attach Chrome, read profiles or establish host acceptance.
- [scripts/test_browser_control.mjs](../../scripts/test_browser_control.mjs) — `test('conflicting, repeated, missing and unknown options fail closed'`; S4. Conflicting/repeated/missing/nontext/unknown attachment options reject before transport creation. Pure argument fixtures do not attach Chrome, read profiles or establish host acceptance.
- [scripts/test_browser_control.mjs](../../scripts/test_browser_control.mjs) — `test('endpoint validation rejects remote hosts, normalized aliases and URL metadata'`; S4. Remote/alias/credential/path/query/fragment/invalid-port endpoint spellings reject without echoing the supplied secret value. Pure argument fixtures do not attach Chrome, read profiles or establish host acceptance.
- [scripts/test_browser_control.mjs](../../scripts/test_browser_control.mjs) — `test('argument parsing is deterministic and cannot mutate inputs or later calls'`; S4. Option parsing is deterministic, read-only and isolated from prior result mutation. Pure argument fixtures do not attach Chrome, read profiles or establish host acceptance.
- [internal/gateway/proxy_diagnostics_test.go](../../internal/gateway/proxy_diagnostics_test.go) — `func TestProxyDiagnosticsClassifyWithoutLeakingRequestOrError(`; S1. Failure categories contain no raw request/error details and preserve 502 behavior.
- [internal/setup/github_diagnostics_test.go](../../internal/setup/github_diagnostics_test.go) — `func TestDiagnosticsUseLocalGitHubCredentialAndRespectReconciliationCadence(`; S1. Diagnostics use local credentials without a GitHub API probe and align freshness/paused/deferred status with periodic reconciliation.
- [internal/commandenv/providers_test.go](../../internal/commandenv/providers_test.go) — `func TestSelectedProvidersWinWithoutGlobalReplacement(`; S1. Selected executables win over conflicting fallback commands, unusual paths and literal arguments are preserved, and previous launcher versions remain unchanged.
- [internal/commandenv/providers_test.go](../../internal/commandenv/providers_test.go) — `func TestUnavailableProvidersNeverFallBack(`; S1. Missing or relative selected providers fail instead of invoking an older system fallback.
- [internal/commandenv/providers_test.go](../../internal/commandenv/providers_test.go) — `func TestProviderDirectoryRejectsEscapingSymlinks(`; S1. A replaced launcher symlink cannot overwrite an outside owner file.
- [internal/gateway/dependencies_test.go](../../internal/gateway/dependencies_test.go) — `func TestDependencyAdminIsAbsentFromPublicGateway(`; S5. Plain and prefixed dependency routes remain unavailable through the public OAuth gateway.
- [internal/codex/compatibility_test.go](../../internal/codex/compatibility_test.go) — `func TestStableCodexVersionBoundary(`; S5. Stable minimum and numeric ordering accept upgrade candidates while rejecting older/prerelease CLI versions.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogRecordsMCPParametersMethodsFailuresAndRedactsSecrets(`; S4. Actual MCP tool/resource and rejected requests retain call names, safe parameters and durations while omitting owner credentials, stored secrets, answers, message text and URL secrets; files and directory are private.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogOwnerParametersTimingAndUnchangedRequestBody(`; S4. Owner request parameters and processing duration are recorded while the downstream handler sees unchanged request bytes and status.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogRotationConcurrencyAndFailurePreserveAPI(`; S4. Actual queued linked-path log failure is observed before asserting unchanged HTTP success and preserved owner file; direct logging fixtures cancel and join all owned workers before storage disposal.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogBoundsMalformedBodiesAndPreservesPanic(`; S4. Malformed/oversized data and panic text never enter records; actual body consumption and panic propagation are preserved with bounded log records.
- [internal/factory/service_test.go](../../internal/factory/service_test.go) — `func TestConfigurationConfirmationStalenessAndSafeBoundary(`; S5. Explicit confirmation rejects stale app proposals and waits until active turns finish.
- [internal/setup/gateway_profile_backup_test.go](../../internal/setup/gateway_profile_backup_test.go) — `func TestLegacyCutoverRestoresDatabaseAbsenceDuringRecoveryAndRollback(`; S1, S2. An actual failed install from legacy-only state records wrapper-database absence, removes partial new state, restores the old release/profile and preserves legacy database/credentials. Explicit rollback from that persisted record archives new wrapper data, removes wrapper files and clears the consumed absence marker.
- [internal/setup/gateway_profile_backup_test.go](../../internal/setup/gateway_profile_backup_test.go) — `func TestRollbackMissingDatabaseSnapshotFailsBeforeStoppingServices(`; S2. A missing recorded database snapshot reports its specific error before any systemctl invocation.
- [internal/setup/diagnostics_operations_test.go](../../internal/setup/diagnostics_operations_test.go) — `func TestFetchDiagnosticsPollsAcceptedOperationAndPreservesFailure(`; S4. Doctor retrieves an accepted diagnostics result using its exact operation identity; delayed failures remain failures and diagnostics is submitted once.
- [internal/transport/response_budget_test.go](../../internal/transport/response_budget_test.go) — `func TestAPILogSlowSinkPreservesSuccessAndLateBusinessLogs503(`; S4. Delayed diagnostic sink does not delay completed200; late business work records actual503 rather than discarded success.
- [internal/transport/response_budget_test.go](../../internal/transport/response_budget_test.go) — `func TestAPILogVaultPoolDelayDoesNotBlockFinishedResponse(`; S4. An exhausted vault read pool cannot delay an already completed successful response; diagnostics later record actual status.
- [internal/transport/response_budget_test.go](../../internal/transport/response_budget_test.go) — `func TestAPILogUncommittedPanicAndSlowSinkNeverInventWire500(`; S4. Uncommitted panic records preserve handler500 separately and actual503 when known, otherwise explicit unknown wire status0.
- [internal/gateway/response_budget_test.go](../../internal/gateway/response_budget_test.go) — `func TestGatewayAuditKeepsActualWireStatusWithSlowSink(`; S4. Production gateway audit sink cannot delay completed success and records actual wire200/503.
- [internal/httpbudget/budget_test.go](../../internal/httpbudget/budget_test.go) — `func TestCapacityRejectionAuditBoundsOverflowAndDrainsActualWorker(`; S4. A blocked diagnostic sink retains one in-flight record,32 queued records and one exact-count overflow aggregate; rejected handlers never run, nested boundaries audit once and service cancellation/drain track actual sink exit.
- [internal/httpbudget/budget_test.go](../../internal/httpbudget/budget_test.go) — `func TestCoalescedRejectionIntervalSurvivesOutOfOrderEnqueue(`; S4. Aggregate UTC interval uses actual earliest and latest request starts despite opposite lock-acquisition order.
- [internal/httpbudget/budget_test.go](../../internal/httpbudget/budget_test.go) — `func TestRejectionAuditIncludesCanceledBeforeAdmission(`; S4. A request rejected for canceled service lifetime records request-not-started without running business work.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogCapacityRejectionsReachDaemonAndExportedBoundary(`; S4. Real daemon mux and exported handlers record each ordinary capacity rejection exactly once with truthful503 status, safe unread metadata and no private query/body/unknown-route values.
- [internal/gateway/audit_test.go](../../internal/gateway/audit_test.go) — `func TestCapacityRejectionsReachProductionGatewayAuditOnce(`; S4. Nested production public/private gateway boundaries emit one sanitized503 request-not-started audit per rejected call with fixed route labels.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogQueueDropsAndRecordsOutcomeWithoutCommandPayload(`; S5. A held log sink fills the bounded queue; the later record reports dropped count, queued outcome and response size without writing the command body.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogWorkersFinishBeforeFixtureStorageDisposal(`; S5. Both Handler log workers finish after cancellation before fixture storage disposal, including queued records behind a held sink; admission remains nonblocking.
- [internal/setup/diagnostics_operations_test.go](../../internal/setup/diagnostics_operations_test.go) — `func TestFetchDiagnosticsReadsExactChunksAndBusyStatusWithoutResubmission(`; S5. Doctor retains one operation identity through temporary busy status and chunk reads, reconstructs a large exact Unicode report, and rejects a corrupt document version without resubmitting diagnostics.
- [internal/setup/diagnostics_operations_test.go](../../internal/setup/diagnostics_operations_test.go) — `func TestFetchDiagnosticsBoundsEntirePendingOperation(`; S4. Repeated fast running/busy responses are bounded by one total diagnostics deadline, independent of individual HTTP request timeouts.
- [internal/transport/api_log_test.go](../../internal/transport/api_log_test.go) — `func TestAPILogLargeRPCBacklogHasByteBoundAndReleasesReplayBodies(`; S5. Two authenticated Handler instances share8MiB captured-buffer admission;64near-limit rejected-tool calls behind a blocked sink retain less than32MiB heap over baseline, release replay bodies and later report byte-pressure drops without raw command text.

## Verification and gaps

Local suites: `browser`, `go`, `release`.
Fixture success does not establish real-host acceptance.
- **S3** — `gap-flow-operate-installation-s3-01` · Unverified behavior · P1 · owner: specification-coordinator. A coordinated real desktop logout/login is still open. Isolated systemd tests and process fixtures cannot prove the current user login cycle. Next verification: Coordinate a real desktop logout/login for the installed version and record service, browser-session and connection continuity observations; isolated process/systemd fixtures cannot close that check.
- **S4** — `gap-flow-operate-installation-s4-01` · Unverified behavior · P1 · owner: specification-coordinator. Controller option fixtures establish only configured local attachment arguments, not sign-in or actual browser/app acceptance. This development port establishes no installed-host callback, soak, login or CSP acceptance; outstanding checks remain unverified in ../acceptance.md. Next verification: Inspect actual-host callback receipt and CSP behavior and complete a connected one-hour soak for the installed port version; update dated acceptance evidence while retaining unobserved checks as unverified.
- **S1** — `gap-flow-operate-installation-s1-01` · Unverified behavior · P2 · owner: user-flow-maintainer. The hard-kill fixture covers only the boundary after release activation and before installation commit at the process-backed service-restart gate. Other interruption stages, power-loss or storage-durability failures and recovery through real user systemd remain unverified. The separate returned-failure fixture completes deferred automatic recovery before rollback and does not establish interruption coverage at additional stages. Next verification: Extend interruption tests to additional durable-operation boundaries and storage/power-loss failure models; separately verify recovery through real user systemd without broadening the existing single-boundary fixture claim.

<!-- /flow-references -->
