import { test, expect } from "@playwright/test";
import type { Snapshot } from "../src/types";
import { initial, host, retained, reportFixture } from "./fixtures";

test("reported issues shares ledger search, information requests and evidence recovery", async ({
  page,
}) => {
  const report = reportFixture();
  const { ui, calls } = await host(page, { reports: [report] });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail).toContainText("What exact response did Work display?");
  await expect(detail).toContainText("Stable availability: Unknown");
  await expect(detail).toContainText(
    "Observed installed Code Factory version: Unknown",
  );
  await ui
    .getByLabel("What did the host display?")
    .fill("Work displayed an expired request error");
  await ui.getByRole("button", { name: "Add evidence", exact: true }).click();
  await expect(detail).toContainText("Work displayed an expired request error");
  await expect(detail.getByRole("definition").first()).toHaveText("queued");
  expect(
    calls.find((c) => c.name === "add_issue_evidence")?.arguments,
  ).toMatchObject({
    id: "report-1",
    text: "Work displayed an expired request error",
  });
  expect(
    calls.find((c) => c.name === "add_issue_evidence")?.arguments
      .idempotencyKey,
  ).toBeTruthy();
  await ui.getByLabel("Search reports").fill("missing");
  await expect(ui.getByLabel("Report list")).toContainText("No reports match");
  await ui.getByLabel("Search reports").fill("stale");
  await expect(
    ui.getByRole("button", { name: "Agents: Stale response" }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Refresh issues and releases" }).click();
  await expect
    .poll(() => calls.some((c) => c.name === "refresh_issue_reports"))
    .toBeTruthy();
  expect(calls.some((c) => c.name === "start_run")).toBeFalsy();
});

test("reported issues exposes rendered draft limitations and required repository fields", async ({ page }) => {
  const report = reportFixture();
  report.findings!.missingInformation = "Please provide the required repository fields: Version";
  const { ui, calls, state } = await host(page, { reports: [report] });
  await ui.getByRole("button", { name: "Reported issues", exact: true }).click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail.getByRole("status")).toContainText("required repository fields: Version");
  await detail.getByLabel("What did the host display?").fill("Version 1.2.3");
  await detail.getByRole("button", { name: "Add evidence", exact: true }).click();
  await expect(detail).toContainText("Version 1.2.3");
  report.processing = "filing";
  report.findings = {
    classification: "suspected", summary: "Delivery regression", missingInformation: "",
    repository: "dk-code-factory/code-factory-plugin", title: "Answer delivery fails",
    body: "Layout limitation: Repository issue layout is unsupported: complex Markdown layout.\n\n## Environment\nFixture host\n\n## Reproduction\nUnknown; observed once",
  };
  state.cursor++;
  await expect(detail.getByRole("status")).toContainText("Repository issue layout is unsupported");
  await detail.getByText("Issue draft", { exact: true }).click();
  await expect(detail.getByRole("heading", { name: "Answer delivery fails" })).toBeVisible();
  await expect(detail.locator("pre")).toContainText("## Environment");
  await expect(detail.locator("pre")).toContainText("Unknown; observed once");
  report.processing = "needs_attention";
  report.message = "Investigation finalized with unusable findings; resume to start a fresh investigation";
  state.cursor++;
  await expect(detail).toContainText("resume to start a fresh investigation");
  await detail.getByLabel("Recovery note").fill("Inspected the finalized investigation; no publication was attempted");
  await detail.getByRole("checkbox", { name: "I inspected the retained workflow" }).check();
  await detail.getByRole("button", { name: "Resume or reconcile report" }).click();
  await expect(detail).toContainText("Recovery admitted");
  expect(calls.find(c => c.name === "resume_issue_report")?.arguments).toMatchObject({ id: report.id, reconciled: true });
});

test("reported issues retries uncertain evidence without duplicating it and accepts edited evidence", async ({
  page,
}) => {
  const report = reportFixture();
  const accepted = new Map<string, string>();
  let attempts = 0;
  const { ui, state, calls } = await host(page, {
    reports: [report],
    addEvidence: async (result, args) => {
      attempts++;
      const key = String(args.idempotencyKey);
      const text = String(args.text);
      if (accepted.has(key) && accepted.get(key) !== text)
        return {
          isError: true,
          content: [
            { type: "text", text: "Evidence key conflicts with accepted text" },
          ],
        };
      if (!accepted.has(key)) {
        accepted.set(key, text);
        result.additions.push(text);
        result.processing = "queued";
        state.cursor++;
      }
      // The daemon accepted the first payload, but both acknowledgements are lost.
      if (attempts <= 2)
        return {
          isError: true,
          content: [{ type: "text", text: "Evidence acknowledgement lost" }],
        };
      return {
        content: [],
        structuredContent: { data: structuredClone(result) },
      };
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  const evidence = detail.getByLabel("What did the host display?");
  const add = detail.getByRole("button", { name: "Add evidence", exact: true });
  const original = "Work displayed an expired request error";
  await evidence.fill(original);
  await add.click();
  await expect(ui.getByRole("alert")).toContainText(
    "Evidence acknowledgement lost",
  );
  await expect(evidence).toHaveValue(original);
  await expect(detail.locator("p").filter({hasText:original})).toHaveCount(1);
  await add.click();
  await expect.poll(() => attempts).toBe(2);
  await expect(add).toBeEnabled();
  await expect(evidence).toHaveValue(original);
  expect(report.additions).toEqual([original]);
  const additions = () => calls.filter((c) => c.name === "add_issue_evidence");
  expect(additions()[1].arguments.idempotencyKey).toBe(
    additions()[0].arguments.idempotencyKey,
  );
  const edited = "Work displayed a newer permission error";
  await evidence.fill(edited);
  await add.click();
  await expect(evidence).toHaveValue("");
  await expect(detail.getByText(edited, { exact: true })).toBeVisible();
  expect(report.additions).toEqual([original, edited]);
  expect(additions()[2].arguments.idempotencyKey).not.toBe(
    additions()[0].arguments.idempotencyKey,
  );
});

test("reported issues displays independent closure, prerelease delivery and explicit workflow recovery", async ({
  page,
}) => {
  const report = reportFixture();
  report.processing = "needs_attention";
  report.outcome = "filed";
  report.issue = {
    repository: "dk-code-factory/code-factory-plugin",
    number: 17,
    url: "https://github.com/dk-code-factory/code-factory-plugin/issues/17",
    state: "closed",
    closureReason: "completed",
  };
  report.releases = [
    {
      repository: "dk-code-factory/agent-workflow",
      version: "v2.0.0-rc1",
      componentVersion: "2.0.0-rc1",
      publishedAt: "2026-10-25T12:00:00Z",
      url: "https://github.com/dk-code-factory/agent-workflow/releases/tag/v2.0.0-rc1",
      commit: "abc",
      prerelease: true,
    },
  ];
  report.releases[0].prerelease = true;
  report.releaseSync.error = "GitHub rate limit";
  const { ui, calls, openLinks } = await host(page, { reports: [report] });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail).toContainText(
    "State: closed · Closure reason: completed",
  );
  await expect(detail).toContainText("Linked fix: Unknown");
  await expect(detail).toContainText("Stable availability: Unknown");
  await expect(detail).toContainText(
    "Prerelease availability: dk-code-factory/agent-workflow v2.0.0-rc1",
  );
  await expect(detail).toContainText("GitHub rate limit");
  const resume = ui.getByRole("button", { name: "Resume or reconcile report" });
  await expect(resume).toBeDisabled();
  await ui
    .getByLabel("Recovery note")
    .fill("Inspected stopped workflow attempts and verified no pending writes");
  await ui
    .getByRole("checkbox", { name: "I inspected the retained workflow" })
    .check();
  await resume.click();
  await expect(detail).toContainText("Recovery admitted");
  expect(
    calls.find((c) => c.name === "resume_issue_report")?.arguments,
  ).toMatchObject({ id: "report-1", reconciled: true });
  await ui.getByRole("button", { name: "Open GitHub issue #17" }).click();
  await expect.poll(() => openLinks).toEqual([report.issue.url]);
});

test("reported issues makes detail progress while journal updates continue", async ({
  page,
}) => {
  const report = reportFixture();
  report.processing = "needs_attention";
  report.findings!.summary = "Slow detail findings";
  const pendingReads: (() => void)[] = [];
  const { ui, state, calls } = await host(page, {
    reports: [report],
    readReport: async (result) => {
      await new Promise<void>((resolve) => {
        pendingReads.push(resolve);
      });
      return result;
    },
  });
  async function journalUpdate() {
    const lists = calls.filter((c) => c.name === "list_issue_reports").length;
    state.cursor++;
    await expect
      .poll(() => calls.filter((c) => c.name === "list_issue_reports").length)
      .toBeGreaterThan(lists);
  }
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  await expect.poll(() => pendingReads.length).toBe(1);
  // Each response is slower than multiple actual journal polling cycles.
  await journalUpdate();
  await journalUpdate();
  pendingReads[0]();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail).toContainText("Slow detail findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs attention",
  );
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
  await expect.poll(() => pendingReads.length).toBe(2);
  await journalUpdate();
  await journalUpdate();
  await expect(detail).toContainText("Slow detail findings");
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
  pendingReads[1]();
  await expect.poll(() => pendingReads.length).toBe(3);
  pendingReads[2]();
});

test("reported issues keeps newer journal details after a delayed mutation read", async ({
  page,
}) => {
  const report = reportFixture();
  const state = initial();
  report.processing = "blocked";
  let releaseRead!: () => void;
  const pendingRead = new Promise<void>((resolve) => {
    releaseRead = resolve;
  });
  let delayNext = false;
  let delayed = false;
  const { ui } = await host(page, {
    snapshot: state,
    reports: [report],
    resumeReport: async (result) => {
      // Deliver the journal event only after the mutation read is pending.
      state.cursor--;
      return result;
    },
    readReport: async (result) => {
      if (delayNext) {
        delayNext = false;
        delayed = true;
        await pendingRead;
      }
      return result;
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail.getByRole("definition").first()).toHaveText("blocked");
  delayNext = true;
  await detail
    .getByRole("button", { name: "Resume or reconcile report" })
    .click();
  await expect.poll(() => delayed).toBe(true);
  report.processing = "needs_attention";
  report.findings!.summary = "Newer journal findings";
  state.cursor++;
  await expect(detail).toContainText("Newer journal findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs attention",
  );
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
  releaseRead();
  await expect(
    ui.getByRole("button", { name: "Refresh issues and releases" }),
  ).toBeEnabled();
  await expect(detail).toContainText("Newer journal findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs attention",
  );
  await expect(detail.getByLabel("Recovery note")).toBeVisible();
});

test("reported issues keeps a pending selection read when an older selection mutation finishes", async ({
  page,
}) => {
  const oldReport = reportFixture();
  oldReport.processing = "blocked";
  const selectedReport = reportFixture("report-2");
  selectedReport.observation.actual = "Selected report response";
  selectedReport.findings!.summary = "Selected report findings";
  let releaseMutation!: () => void;
  let releaseRead!: () => void;
  const pendingMutation = new Promise<void>((resolve) => {
    releaseMutation = resolve;
  });
  const pendingRead = new Promise<void>((resolve) => {
    releaseRead = resolve;
  });
  let mutationPending = false;
  let readPending = false;
  const { ui, calls } = await host(page, {
    reports: [oldReport, selectedReport],
    resumeReport: async (result) => {
      mutationPending = true;
      await pendingMutation;
      return result;
    },
    readReport: async (result) => {
      if (result.id === selectedReport.id) {
        readPending = true;
        await pendingRead;
      }
      return result;
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  await expect(detail.getByRole("definition").first()).toHaveText("blocked");
  const reads = calls.filter((c) => c.name === "get_issue_report").length;
  await detail
    .getByRole("button", { name: "Resume or reconcile report" })
    .click();
  await expect.poll(() => mutationPending).toBe(true);
  // Consume the mutation journal event before starting the new selection read.
  await expect
    .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
    .toBeGreaterThan(reads);
  await ui
    .getByRole("button", { name: "Agents: Selected report response" })
    .click();
  await expect.poll(() => readPending).toBe(true);
  releaseMutation();
  await expect(
    ui.getByRole("button", { name: "Refresh issues and releases" }),
  ).toBeEnabled();
  releaseRead();
  await expect(detail).toContainText("Report report-2");
  await expect(detail).toContainText("Selected report findings");
  await expect(detail.getByRole("definition").first()).toHaveText(
    "needs information",
  );
  await expect(
    detail.getByRole("button", { name: "Resume or reconcile report" }),
  ).toBeEnabled();
});

test("reported issues releases mutation controls during continuing slow journal refreshes", async ({
  page,
}) => {
  const report = reportFixture();
  let slow = false;
  const pending: (() => void)[] = [];
  const { ui, state, calls } = await host(page, {
    reports: [report],
    listReports: async (result) => {
      if (slow)
        await new Promise<void>((resolve) => {
          pending.push(resolve);
        });
      return result;
    },
  });
  async function journalUpdate() {
    const reads = calls.filter((c) => c.name === "get_issue_report").length;
    state.cursor++;
    await expect
      .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
      .toBeGreaterThan(reads);
  }
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  await ui.getByRole("button", { name: "Agents: Stale response" }).click();
  const detail = ui.getByRole("article", { name: "Report details" });
  const evidence = detail.getByLabel("What did the host display?");
  const refresh = ui.getByRole("button", {
    name: "Refresh issues and releases",
  });
  await expect(evidence).toBeEnabled();
  slow = true;
  await refresh.click();
  await expect.poll(() => pending.length).toBe(1);
  await expect(refresh).toBeEnabled();
  await expect(evidence).toBeEnabled();
  await journalUpdate();
  await journalUpdate();
  pending[0]();
  await expect.poll(() => pending.length).toBe(2);
  // The mutation refresh rendered; a queued journal refresh is still pending.
  await journalUpdate();
  await journalUpdate();
  await expect(refresh).toBeEnabled();
  await expect(evidence).toBeEnabled();
  await expect(
    detail.getByRole("button", { name: "Resume or reconcile report" }),
  ).toBeEnabled();
  await evidence.fill("Evidence supplied while journal refresh is pending");
  await detail.getByRole("button", { name: "Add evidence" }).click();
  await expect(detail).toContainText(
    "Evidence supplied while journal refresh is pending",
  );
  await expect(evidence).toBeEnabled();
  expect(pending).toHaveLength(2);
  slow = false;
  pending[1]();
});

test("reported issues allows pagination during continuing slow journal refreshes", async ({
  page,
}) => {
  const reports = Array.from({ length: 65 }, (_, i) =>
    reportFixture(`report-${String(i).padStart(2, "0")}`),
  );
  reports[59].observation.actual = "Second page report";
  reports[64].observation.actual = "Third page report";
  let slow = false;
  const pending: (() => void)[] = [];
  const { ui, state, calls } = await host(page, {
    reports,
    listReports: async (result) => {
      const response = structuredClone(result);
      if (slow)
        await new Promise<void>((resolve) => {
          pending.push(resolve);
        });
      return response;
    },
  });
  async function journalUpdate() {
    const reads = calls.filter((c) => c.name === "get_issue_report").length;
    state.cursor++;
    await expect
      .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
      .toBeGreaterThan(reads);
  }
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  const list = ui.getByLabel("Report list", { exact: true });
  await expect(list.getByRole("article")).toHaveCount(30);
  await list
    .getByRole("button", { name: "Agents: Stale response" })
    .first()
    .click();
  await expect(
    ui.getByRole("article", { name: "Report details" }),
  ).toBeVisible();
  const more = list.getByRole("button", { name: "Load more reports" });
  slow = true;
  await journalUpdate();
  await expect.poll(() => pending.length).toBe(1);
  await journalUpdate();
  await journalUpdate();
  await expect(more).toBeEnabled();
  await more.dblclick();
  await expect(more).toBeDisabled();
  expect(pending).toHaveLength(1);
  pending[0]();
  await expect.poll(() => pending.length).toBe(2);
  await journalUpdate();
  pending[1]();
  await expect(list.getByRole("article")).toHaveCount(60);
  await expect(list).toContainText("Second page report");
  await expect(list).not.toContainText("Third page report");
  await expect.poll(() => pending.length).toBe(3);
  await expect(more).toBeEnabled();
  // Background refresh remains active after the requested page was rendered.
  await journalUpdate();
  await journalUpdate();
  await expect(more).toBeEnabled();
  expect(pending).toHaveLength(3);
  expect(
    await list.getByRole("article").locator("p").allTextContents(),
  ).toEqual(
    reports
      .slice(0, 60)
      .map((r) => `needs information · ${r.component} · ${r.id}`),
  );
  pending[2]();
  await expect.poll(() => pending.length).toBe(4);
  slow = false;
  pending[3]();
});

test("reported issues paginates and filters processing component and version", async ({
  page,
}) => {
  const reports = Array.from({ length: 32 }, (_, i) =>
    reportFixture(`report-${String(i).padStart(2, "0")}`),
  );
  reports[31].processing = "complete";
  reports[31].component = "plugin";
  reports[31].installedVersion = "1.2.3";
  reports[31].observation.actual = "Final report";
  let releasePage: (() => void) | undefined;
  const pendingPage = new Promise<void>((resolve) => {
    releasePage = resolve;
  });
  let delayed = false;
  const { ui, calls, state } = await host(page, {
    reports,
    listReports: async (result, args) => {
      if (args.cursor && !delayed) {
        delayed = true;
        await pendingPage;
      }
      return result;
    },
  });
  await ui
    .getByRole("button", { name: "Reported issues", exact: true })
    .click();
  const list = ui.getByLabel("Report list", { exact: true });
  await expect(list.getByRole("article")).toHaveCount(30);
  await list
    .getByRole("button", { name: "Agents: Stale response" })
    .first()
    .click();
  await expect(
    ui.getByRole("article", { name: "Report details" }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Load more reports" }).click();
  await expect.poll(() => delayed).toBe(true);
  const reads = calls.filter((c) => c.name === "get_issue_report").length;
  state.cursor++;
  await expect
    .poll(() => calls.filter((c) => c.name === "get_issue_report").length)
    .toBeGreaterThan(reads);
  releasePage!();
  await expect(list.getByRole("article")).toHaveCount(32);
  reports[31].observation.actual = "Updated final report";
  state.cursor++;
  await expect(list).toContainText("Updated final report");
  await expect(list.getByRole("article")).toHaveCount(32);
  expect(
    await list.getByRole("article").locator("p").allTextContents(),
  ).toEqual(
    reports.map(
      (r) => `${r.processing.replaceAll("_", " ")} · ${r.component} · ${r.id}`,
    ),
  );
  await ui.getByLabel("Processing", { exact: true }).selectOption("complete");
  await ui.getByLabel("Component", { exact: true }).fill("plugin");
  await ui.getByLabel("Version", { exact: true }).fill("1.2.3");
  await expect(list.getByRole("article")).toHaveCount(1);
  await expect(list).toContainText("Updated final report");
  await expect
    .poll(() =>
      calls.some(
        (c) =>
          c.name === "list_issue_reports" &&
          c.arguments.status === "complete" &&
          c.arguments.component === "plugin" &&
          c.arguments.version === "1.2.3",
      ),
    )
    .toBeTruthy();
  await ui.getByLabel("Processing", { exact: true }).selectOption("");
  await ui.getByLabel("Component", { exact: true }).fill("");
  await ui.getByLabel("Version", { exact: true }).fill("");
  await expect(list.getByRole("article")).toHaveCount(30);
  await expect(
    ui.getByRole("button", { name: "Load more reports" }),
  ).toBeEnabled();
});

test("the shared subscription updates Messages while Reported issues is open",async({page})=>{
  const {ui,state,calls}=await host(page,{watchDomains:["run","attention"]});
  await ui.getByRole("button",{name:"Reported issues",exact:true}).click();
  await expect(ui.getByRole("region",{name:"Reported issues"})).toBeVisible();
  await expect.poll(()=>calls.filter(c=>c.name==="list_issue_reports").length).toBe(1);
  const reportReads=calls.filter(c=>c.name==="list_issue_reports").length;
  state.runs=[retained(state,"new-question","waiting")];
  state.attention=[{id:"new-question-request",runId:"new-question",status:"pending",method:"item/tool/requestUserInput",blocking:true,params:{questions:[{id:"scope",question:"Which scope?",options:[{label:"Focused"},{label:"Broad"}]}]}} as Snapshot["attention"][number]];
  state.cursor++;
  await expect(ui.getByRole("button",{name:"Messages 1",exact:true})).toBeVisible();
  await expect(ui.getByRole("region",{name:"Reported issues"})).toBeVisible();
  const reads=calls.filter(c=>c.name==="read_ui_state");
  expect(reads.some(c=>(c.arguments.views as string[]).includes("messages"))).toBe(true);
  expect(calls.filter(c=>c.name==="list_issue_reports")).toHaveLength(reportReads);
});

test("full resync to an earlier cursor reloads Reports and preserves its evidence draft",async({page})=>{
  const snapshot=initial();snapshot.cursor=100;
  const report=reportFixture();
  const {ui,state}=await host(page,{snapshot,reports:[report],watchDomains:["issue-report"]});
  await ui.getByRole("button",{name:"Reported issues",exact:true}).click();
  await ui.getByRole("button",{name:"Agents: Stale response"}).click();
  const detail=ui.getByRole("article",{name:"Report details"});
  const evidence=detail.getByLabel("What did the host display?");
  await evidence.fill("Keep this report draft after database restore.");
  report.findings!.summary="Latest before restore";state.cursor=101;
  await expect(detail).toContainText("Latest before restore");
  report.findings!.summary="Restored report state";state.cursor=3;
  await expect(detail).toContainText("Restored report state");
  await expect(detail).not.toContainText("Latest before restore");
  await expect(evidence).toHaveValue("Keep this report draft after database restore.");
});

for (const outcome of ["invalid", "resolved"]) {
  test(`reported issues shows auditor ${outcome} outcome without claiming delivery`, async ({
    page,
  }) => {
    const report = reportFixture();
    report.processing = "complete";
    report.outcome = outcome;
    report.findings = {
      classification: outcome,
      summary:
        outcome === "invalid"
          ? "Source contract confirms expected behavior."
          : "Verified fixing commit is present in the inspected revision.",
      missingInformation: "",
    };
    if (outcome === "resolved") {
      report.issue = {
        repository: "dk-code-factory/code-factory-plugin",
        number: 17,
        url: "https://github.com/dk-code-factory/code-factory-plugin/issues/17",
        state: "closed",
        closureReason: "completed",
      };
      report.fixes = [
        {
          repository: report.issue.repository,
          component: "code-factory",
          commit: "b".repeat(40),
        },
      ];
    }
    const { ui, calls } = await host(page, { reports: [report] });
    await ui
      .getByRole("button", { name: "Reported issues", exact: true })
      .click();
    await ui.getByRole("button", { name: "Agents: Stale response" }).click();
    const detail = ui.getByRole("article", { name: "Report details" });
    await expect(detail.getByRole("definition").nth(1)).toHaveText(
      outcome === "invalid" ? "Invalid report" : "Already resolved",
    );
    await expect(detail).toContainText(report.findings.summary);
    await expect(detail).toContainText("Stable availability: Unknown");
    await expect(detail).toContainText(
      "Observed installed Code Factory version: Unknown",
    );
    await expect(detail.getByRole("button", { name: "Resume" })).toHaveCount(0);
    await expect(detail.getByText("Information requested:")).toHaveCount(0);
    if (outcome === "resolved") {
      await expect(detail).toContainText(
        "Fix verified in the investigated source revision.",
      );
      await expect(
        detail.getByRole("button", { name: "Open fix commit" }),
      ).toBeVisible();
    } else {
      await expect(
        detail.getByRole("button", { name: /Open GitHub issue/ }),
      ).toHaveCount(0);
    }
    expect(
      calls.some((c) =>
        ["start_run", "resume_issue_report", "refresh_issue_reports"].includes(
          c.name,
        ),
      ),
    ).toBe(false);
  });
}
