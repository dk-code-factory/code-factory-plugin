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
  id: string;
  name: string;
  path: string;
  content: string;
  managed: boolean;
}
export interface Repository {
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
  id: string;
  priorityLabels: Record<string, number>;
  assessmentPrompt: string;
  policyVersion: string;
  jiraUrl: string;
  jiraEmail: string;
  notifications: boolean;
}
export interface Snapshot {
  cursor: number;
  inbox: Entry[];
  repositories: Repository[];
  profiles: Profile[];
  runs: Run[];
  skills: Skill[];
  attention: Attention[];
  config: Config;
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
  item: Item;
  user: UserState;
  relationships: {
    id: string;
    source: string;
    target: string;
    evidence: string;
    manual: boolean;
  }[];
  tickets: { id: string; summary: string; status: string; error: string }[];
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
