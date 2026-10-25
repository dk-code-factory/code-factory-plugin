export interface Item {
  id: string;
  repository: string;
  number: number;
  kind: string;
  title: string;
  body: string;
  url: string;
  state: string;
  author: string;
  assignees: string[];
  labels: string[];
  draft: boolean;
  head: string;
  branch: string;
  revision: string;
  updated: string;
  activity: {
    id: string;
    kind: string;
    author: string;
    body: string;
    url: string;
  }[];
  checks?: unknown;
  pr?: PullRequest;
  reviewRequests?: string[];
  reviewTeams?: string[];
}
export interface UserState {
  new: boolean;
  readRevision: string;
  override: number | null;
  actions: { id: string; kind: string; at: string }[];
}
export interface Profile {
  mode?: "default" | "plan";
  id: string;
  name: string;
  prompt: string;
  skills: string[];
  model: string;
  effort: string;
  sandbox: string;
  approval: string;
  publishing: string;
}
export interface Skill {
  disabled?: boolean;
  description?: string;
  id: string;
  name: string;
  path: string;
  content: string;
  managed: boolean;
}
export interface Repository {
  paused?: boolean;
  autoReview?: boolean | null;
  reviewProfileId?: string;
  implementationProfileId?: string;
  reviewPrompt?: string;
  reviewError?: string;
  id: string;
  checkout: string;
  base: string;
  lastSync: string;
  error: string;
}
export interface Run {
  id: string;
  itemId: string;
  profile: Profile;
  prompt: string;
  skills: Skill[];
  worktree: string;
  branch: string;
  base: string;
  threadId: string;
  turnId: string;
  status: string;
  error: string;
  created: string;
}
export interface Attention {
  id: string;
  runId: string;
  method: string;
  blocking: boolean;
  status: string;
  origin?: "rpc" | "async" | "text";
  turnId?: string;
  itemId?: string;
  recoveryId?: string;
  params: {
    questions?: Question[];
    availableDecisions?: unknown[];
    command?: string;
    reason?: string;
    [key: string]: unknown;
  };
}
export interface Question {
  id: string;
  header: string;
  question: string;
  isOther?: boolean;
  isSecret?: boolean;
  options?: { label: string; description: string }[];
}
export interface Entry {
  hasPullRequest?: boolean;
  queues?: string[];
  work?: {
    tracked: boolean;
    complete: boolean;
    status: string;
    runIds: string[];
    attentionCount: number;
  };
  linkedPullRequests?: PullRequest[];
  item: Item;
  user: UserState;
  unread: boolean;
  priority: number | null;
  prioritySource: string;
  assessment: {
    status: string;
    rationale: string;
    confidence: number;
    error: string;
  };
}
export interface Config {
 githubRequestsPerHour?: number;
 githubReserve?: number;
 githubPollSeconds?: number;
  autoReview?: boolean;
  reviewProfileId?: string;
  implementationProfileId?: string;
  pluginCheckMinutes?: number;
  id: string;
  priorityLabels: Record<string, number>;
  assessmentPrompt: string;
  policyVersion: string;
  jiraUrl: string;
  jiraEmail: string;
  notifications: boolean;
  questionHelperModel?: string;
}
export interface GitHubUsage {
 notificationError?:string;
 requestsLastHour:number; budget:number; reserve:number; nextRequest:string;
 restRemaining?:number;restReset:string;graphqlRemaining?:number;graphqlReset:string;
}
export interface Snapshot {
  reviewVersions?: Record<string, string>;
 githubUsage?:GitHubUsage;
  viewer?: { login: string; teams: string[] | null; teamError?: string };
  cursor: number;
  inbox: Entry[];
  repositories: Repository[];
  profiles: Profile[];
  runs: Run[];
  skills: Skill[];
  attention: Attention[];
  questionRecoveries?: QuestionRecovery[];
  config: Config;
}
export interface PublishedReview {
  id: string;
  url: string;
  author: string;
  state: string;
  body: string;
  head: string;
  submittedAt: string;
  comments: {id: string; url: string; author: string; body: string; path: string; line?: number}[];
}
export interface PRReviews {
 refresh?: string;
 retryAt?: string;
  itemId: string;
  reviews: PublishedReview[];
  syncedAt: string;
  lastAttempt: string;
  error?: string;
}
export interface JournalEvent {
  seq: number;
  kind: string;
  entity: string;
  data: { method?: string; params?: Record<string, unknown> };
  at: string;
}
export interface EventPage {
  events: JournalEvent[];
  cursor: number;
  hasMore: boolean;
}
export interface Detail {
 observation?: {observedAt:string;refresh:string;error?:string;retryAt?:string};
 refreshError?: string;
  item: Item;
  user: UserState;
  linkedPullRequests?: PullRequest[];
  relationships: {
    id: string;
    source: string;
    target: string;
    evidence: string;
    manual: boolean;
  }[];
  tickets: { id: string; summary: string; status: string; error: string }[];
}

export interface PullRequest {
  id: string;
  title: string;
  url: string;
  state: string;
  draft: boolean;
  head: string;
  reviewDecision: string;
  ci: string;
  checks: { name: string; status: string; conclusion: string; url: string }[];
  syncedAt: string;
  error?: string;
  implementation?: boolean;
}
export interface QuestionRecovery {
  sourceText?: string;
  question?: string;
  confirmation?: string;
  originalBlocking?: boolean;
  diagnosticError?: string;
  textAttentionId?: string;
  id: string;
  runId: string;
  stage: string;
  attempts: number;
}

export interface Diagnostics {
  version: string;
  at: string;
  checks: {
    id: string;
    state: "verified" | "failed" | "unverified";
    detail: string;
    at: string;
  }[];
}

export interface IssueReport {
  id: string;
  observation: {
    surface: string;
    expected: string;
    actual: string;
    steps?: string[];
    reproductionUnknown?: string;
    observedAt: string;
    pluginVersion?: string;
    impact?: string;
    frequency?: string;
    suspectedCause?: string;
  };
  processing: string;
  outcome: string;
  component: string;
  message: string;
  additions: string[];
  workflowId: string;
  versions: Record<string, string>;
  installedVersion: string;
  findings?: {
    classification: string;
    summary: string;
    missingInformation: string;
    repository: string;
  };
  issue?: {
    url: string;
    state: string;
    closureReason: string;
    repository: string;
    number: number;
  };
  fixes: {
    repository: string;
    pr?: string;
    commit: string;
    component: string;
    version?: string;
  }[];
  releases: {
    repository: string;
    version: string;
    componentVersion: string;
    publishedAt: string;
    url: string;
    prerelease: boolean;
    commit: string;
  }[];
  evidence: {
    origin: string;
    sha256?: string;
    omission?: string;
    truncated: boolean;
    at: string;
    bytes: number;
  }[];
  history: { status: string; detail: string; at: string }[];
  issueSync: ReportFreshness;
  releaseSync: ReportFreshness;
}
export interface ReportFreshness {
  lastSuccess: string;
  lastAttempt: string;
  retryAt: string;
  error: string;
}
export interface ReportPage {
  items: IssueReport[];
  nextCursor: string;
}
