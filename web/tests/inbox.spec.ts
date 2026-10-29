import { test, expect } from "@playwright/test";
import type { Snapshot, Run, JournalEvent, PRReviews } from "../src/types";
import { initial, host, showSettings, openItem, showItemDetails, showAgents, openWaiting, linkTarget, triage, tabTo, scanOwnedApp, retained } from "./fixtures";

test("item actions refresh visible history while unread, New and drafts remain independent", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  const row = ui.getByRole("button", { name: /Fix reconnect regression/ });
  await row.click();
  await expect(ui.locator(".item-list")).toHaveCount(0);
  await expect.poll(() => state.inbox[0].unread).toBe(false);
  expect(state.inbox[0].user.new).toBe(true);
  await (await linkTarget(ui)).fill("owner/repo#draft");
  await ui.getByLabel("Priority override").selectOption("2");
  await triage(ui, "Record action");
  await expect(
    ui.locator(".detail p").filter({ hasText: /^plugin ·/ }),
  ).toHaveCount(1);
  expect(state.inbox[0].user.new).toBe(true);
  await triage(ui, "Mark unread");
  await expect.poll(() => state.inbox[0].unread).toBe(true);
  await triage(ui, "Acknowledge new");
  await expect.poll(() => state.inbox[0].user.new).toBe(false);
  expect(state.inbox[0].unread).toBe(true);
  await expect(ui.getByLabel("Link target")).toHaveValue("owner/repo#draft");
  await expect(ui.getByLabel("Priority override")).toHaveValue("2");
  expect(
    calls.filter(
      (c) => c.name === "item_action" && c.arguments.action === "read",
    ),
  ).toHaveLength(1);
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await expect(row.locator(".dot")).toHaveCount(1);
  await expect(row.locator("..").getByText("New", { exact: true })).toHaveCount(
    0,
  );
  await expect(row.locator("..")).toContainText("Acted on");
  expect(
    calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name)),
  ).toHaveLength(0);
});

test("unlink removes the visible relationship without clearing unrelated detail drafts", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await openItem(ui, /Fix reconnect regression/);
  await (await linkTarget(ui)).fill("owner/repo#42");
  await ui.getByRole("button", { name: "Add local link", exact: true }).click();
  const link = ui.locator(".detail p").filter({ hasText: "owner/repo#42" });
  await expect(link).toContainText("Local link");
  await (await linkTarget(ui)).fill("owner/repo#next-draft");
  await ui.getByLabel("Priority override").selectOption("3");
  await link.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(link).toHaveCount(0);
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", level: 1 }),
  ).toBeVisible();
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#next-draft",
  );
  await expect(ui.getByLabel("Priority override")).toHaveValue("3");
  expect(
    calls.filter(
      (call) => call.name === "set_relationship" && call.arguments.remove,
    ),
  ).toHaveLength(1);
});

test("a delayed relationship refresh cannot replace a later selection or its drafts", async ({
  page,
}) => {
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second retained item";
  snapshot.inbox.push(second);
  let release!: () => void;
  let delayed = false;
  let completed = false;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui } = await host(page, {
    snapshot,
    readItem: async (detail) => {
      if (detail.item.id === "owner/repo#1" && detail.relationships.length) {
        delayed = true;
        await gate;
        completed = true;
      }
      return detail;
    },
  });
  try {
    await openItem(ui, /Fix reconnect regression/);
    await (await linkTarget(ui)).fill("owner/repo#42");
    await ui
      .getByRole("button", { name: "Add local link", exact: true })
      .click();
    await expect.poll(() => delayed).toBe(true);
    await openItem(ui, /Second retained item/);
    await expect(
      ui.getByRole("heading", { name: "Second retained item", level: 1 }),
    ).toBeVisible();
    await (await linkTarget(ui)).fill("owner/repo#second-draft");
    await ui.getByLabel("Priority override").selectOption("2");
    release();
    await expect.poll(() => completed).toBe(true);
    await ui
      .locator("body")
      .evaluate(
        () =>
          new Promise<void>((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
          ),
      );
    await expect(
      ui.getByRole("heading", { name: "Second retained item", level: 1 }),
    ).toBeVisible();
    await expect(ui.getByLabel("Link target")).toHaveValue(
      "owner/repo#second-draft",
    );
    await expect(ui.getByLabel("Priority override")).toHaveValue("2");
  } finally {
    release();
  }
});

test("failed reviews keep failure status and alert instead of GitHub retry controls", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].item.kind = "pr";
  const run = retained(snapshot, "review-failed", "failed");
  run.error = "Review checkout failed.";
  snapshot.runs = [run];
  const { ui } = await host(page, { snapshot });
  await ui.getByRole("button", { name: "Working", exact: true }).click();
  await expect(ui.getByLabel("Work status")).toHaveText("Failed");
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Work status")).toHaveText("Failed");
  await expect(ui.getByRole("alert")).toHaveText(run.error);
  await expect(ui.getByRole("region", { name: "GitHub retry" })).toHaveCount(0);
  await expect(
    ui.getByRole("button", { name: "Cancel retry", exact: true }),
  ).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toBeEnabled();
});

test("item switching restores saved priorities and keeps relationship drafts by item", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].user.override = 3;
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second item";
  second.user.override = 0;
  snapshot.inbox.push(second);
  const { ui, calls } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("3");
  await (await linkTarget(ui)).fill("owner/repo#draft-one");
  await openItem(ui, /Second item/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("0");
  await expect(ui.getByLabel("Link target")).toHaveValue("");
  await (await linkTarget(ui)).fill("owner/repo#draft-two");
  await ui.getByLabel("Priority override").selectOption("2");
  await expect(ui.getByLabel("Priority override")).toBeEnabled();
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("3");
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#draft-one",
  );
  await showAgents(ui);
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#draft-one",
  );
  await openItem(ui, /Second item/);
  await expect(ui.getByLabel("Priority override")).toHaveValue("2");
  await expect(ui.getByLabel("Link target")).toHaveValue(
    "owner/repo#draft-two",
  );
  for (const priority of ["0", "1", "2", "3", ""]) {
    await ui.getByLabel("Priority override").selectOption(priority);
    await expect(ui.getByLabel("Priority override")).toBeEnabled();
  }
  expect(
    calls
      .filter(
        (c) => c.name === "item_action" && c.arguments.action === "priority",
      )
      .map((c) => c.arguments),
  ).toEqual(
    [2, 0, 1, 2, 3, null].map((priority) => ({
      id: second.item.id,
      action: "priority",
      priority,
    })),
  );
});

test("failed immediate priority saves restore the saved value with an inline error", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].user.override = 1;
  const { ui } = await host(page, { snapshot, failPriority: true });
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByLabel("Priority override").selectOption("0");
  await expect(ui.getByLabel("Priority override")).toHaveValue("1");
  await expect(ui.locator(".detail").getByRole("alert")).toContainText(
    "Priority save failed",
  );
});

for (const theme of ["light", "dark"] as const) {
  test(`readable approval choices preserve payloads in a narrow ${theme} embed`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "approval-owner", "waiting")];
    const decision = {
      acceptWithExecpolicyAmendment: {
        execpolicy_amendment: ["git", "status"],
      },
    };
    const choices = [
      { decision: "accept", label: "Allow once" },
      { decision: "acceptForSession", label: "Allow for this session" },
      { decision: "decline", label: "Deny" },
      { decision: "cancel", label: "Cancel" },
      { decision, label: "Allow and update command policy" },
    ];
    const { ui, state, calls } = await host(page, { snapshot, theme });
    for (const [index, choice] of choices.entries()) {
      state.runs[0].status = "waiting";
      state.attention = [
        {
          id: `approval-${index}`,
          runId: "approval-owner",
          method: "item/commandExecution/requestApproval",
          blocking: true,
          status: "pending",
          params: {
            command: "git status",
            reason: "Inspect working tree",
            cwd: "/local/approval-owner",
            availableDecisions: [choice.decision],
          },
        },
      ];
      state.cursor++;
      await openWaiting(ui);
      const dialog = ui.getByRole("dialog");
      await expect(
        dialog.getByText("git status", { exact: true }),
      ).toBeVisible();
      await expect(
        dialog.getByText("Inspect working tree", { exact: true }),
      ).toBeVisible();
      await expect(dialog.locator("details")).not.toHaveAttribute("open", "");
      await expect(dialog.locator("pre")).toBeHidden();
      expect(
        await dialog.evaluate((el) => {
          const r = el.getBoundingClientRect();
          return r.left >= 0 && r.right <= window.innerWidth;
        }),
      ).toBe(true);
      if (index === 0)
        await page.screenshot({
          path: test.info().outputPath(`approval-${theme}.png`),
        });
      expect(
        await ui
          .locator("body")
          .evaluate((b) => b.scrollWidth <= b.clientWidth),
      ).toBe(true);
      await tabTo(
        page,
        dialog.getByRole("button", { name: choice.label, exact: true }),
      );
      await page.keyboard.press("Enter");
      await expect(dialog).toHaveCount(0);
      expect(
        calls.filter((c) => c.name === "answer_request").at(-1)?.arguments,
      ).toEqual({
        id: `approval-${index}`,
        answer: { decision: choice.decision },
      });
    }
    state.runs[0].status = "waiting";
    state.attention = [
      {
        id: "permissions",
        runId: "approval-owner",
        method: "item/permissions/requestApproval",
        blocking: true,
        status: "pending",
        params: {
          reason: "Read project files",
          permissions: { fileSystem: { read: ["/local/project"], write: [] } },
        },
      },
    ];
    state.cursor++;
    await openWaiting(ui);
    await expect(
      ui.getByRole("dialog").getByText("/local/project", { exact: true }),
    ).toBeVisible();
    await ui.getByRole("button", { name: "Allow once", exact: true }).click();
    await expect(ui.getByRole("dialog")).toHaveCount(0);
    expect(
      calls.filter((c) => c.name === "answer_request").at(-1)?.arguments,
    ).toEqual({
      id: "permissions",
      answer: {
        permissions: { fileSystem: { read: ["/local/project"], write: [] } },
        scope: "turn",
      },
    });
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await openItem(ui, /Fix reconnect regression/);
    await ui.getByRole("button", { name: "New run", exact: true }).click();
    const launch = ui.getByRole("dialog", { name: "Launch agent" });
    await expect(
      launch.getByRole("button", { name: "Start agent", exact: true }),
    ).toBeEnabled();
    expect(
      await launch.evaluate((el) => {
        const r = el.getBoundingClientRect();
        return r.left >= 0 && r.right <= window.innerWidth;
      }),
    ).toBe(true);
    await page.screenshot({
      path: test.info().outputPath(`launch-${theme}.png`),
    });
    await launch.getByRole("button", { name: "Edit", exact: true }).click();
    await launch.getByText("Advanced", { exact: true }).click();
    expect(
      await launch.evaluate((el) => {
        const r = el.getBoundingClientRect();
        return r.left >= 0 && r.right <= window.innerWidth;
      }),
    ).toBe(true);
    await page.keyboard.press("Escape");
    await showSettings(page);
    await ui.getByText("Priorities & notifications", { exact: true }).click();
    expect(
      await ui.locator("body").evaluate((b) => b.scrollWidth <= b.clientWidth),
    ).toBe(true);
  });
}

for (const theme of ["light", "dark"] as const) {
  for (const width of [1280, 390]) {
    test(`single-page navigation preserves list context and drafts at ${width}px in ${theme}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 820 });
      const snapshot = initial();
      snapshot.inbox = Array.from({ length: 40 }, (_, index) => {
        const entry = structuredClone(snapshot.inbox[0]);
        entry.item.id = `owner/repo#${index + 1}`;
        entry.item.title = `Item ${index + 1}: investigation`;
        entry.item.body = `Description for item ${index + 1}`;
        return entry;
      });
      snapshot.inbox[0].item.title +=
        ": investigate a lengthy failure description that wraps in a narrow inbox while keeping priority beside the first line";
      snapshot.runs = [
        { ...retained(snapshot, "page-run"), itemId: "owner/repo#30" },
      ];
      const { ui, calls } = await host(page, { snapshot, theme });
      await expect(
        ui.locator("header, footer, .brand, .mark, .split, .detail"),
      ).toHaveCount(0);
      await expect(
        ui.getByRole("status", { name: "Connection status" }),
      ).toHaveClass("sr-only");
      await expect(ui.getByText("Sync details", { exact: true })).toHaveCount(
        0,
      );
      await ui.getByLabel("Search inbox").fill("Item");
      await ui.getByLabel("Inbox filter").selectOption("all");
      const row = ui.getByRole("button", { name: /Item 30: investigation/ });
      const listWidth = await ui
        .locator(".item-list")
        .evaluate((el) => el.getBoundingClientRect().width);
      const frameWidth = await ui
        .locator("body")
        .evaluate(() => window.innerWidth);
      expect(listWidth).toBeGreaterThan(frameWidth * 0.9);
      const firstItem = ui.locator(".inbox-row > .item").first();
      await expect(firstItem.locator(".priority")).toHaveText("P1");
      await expect(ui.locator(".item-meta .priority")).toHaveCount(0);
      const badgeBox = await firstItem.locator(".priority").boundingBox();
      const titleBox = await firstItem.locator(".item-title").boundingBox();
      expect(badgeBox!.x + badgeBox!.width).toBeLessThanOrEqual(titleBox!.x);
      expect(Math.abs(badgeBox!.y - titleBox!.y)).toBeLessThan(6);
      expect(
        await ui
          .locator("body")
          .evaluate((b) => b.scrollWidth <= b.clientWidth),
      ).toBe(true);
      await page.screenshot({
        path: test.info().outputPath(`inbox-top-${width}-${theme}.png`),
      });
      await row.scrollIntoViewIfNeeded();
      await row.focus();
      const scrollBefore = await ui
        .locator("body")
        .evaluate(() => window.scrollY);
      expect(scrollBefore).toBeGreaterThan(0);
      await page.keyboard.press("Enter");
      await expect(
        ui.getByRole("heading", { name: "Item 30: investigation", level: 1 }),
      ).toBeVisible();
      await expect(ui.locator(".item-list")).toHaveCount(0);
      await expect(ui.getByLabel("Search inbox")).toHaveCount(0);
      await expect(
        ui.getByText("Description for item 30", { exact: true }),
      ).toBeVisible();
      await expect(
        ui.getByText("Item 29: investigation", { exact: true }),
      ).toHaveCount(0);
      expect(
        await ui
          .locator("body")
          .evaluate((b) => b.scrollWidth <= b.clientWidth),
      ).toBe(true);
      await page.screenshot({
        path: test.info().outputPath(`item-${width}-${theme}.png`),
      });
      await (await linkTarget(ui)).fill("owner/repo#retained-draft");
      await expect(ui.getByLabel("Message agent")).toBeVisible();
      await expect(ui.locator(".item-list")).toHaveCount(0);
      await ui.getByLabel("Message agent").fill("Run-specific message draft");
      await showItemDetails(ui);
      await expect(ui.getByLabel("Link target")).toHaveValue(
        "owner/repo#retained-draft",
      );
      await ui.getByRole("button", { name: "← Issues", exact: true }).click();
      await expect(ui.locator(".detail")).toHaveCount(0);
      await expect(ui.getByLabel("Search inbox")).toHaveValue("Item");
      await expect(ui.getByLabel("Inbox filter")).toHaveValue("all");
      await expect(row).toBeFocused();
      expect(
        await ui.locator("body").evaluate(() => window.scrollY),
      ).toBeCloseTo(scrollBefore, 0);
      await page.screenshot({
        path: test.info().outputPath(`inbox-${width}-${theme}.png`),
      });
      await showAgents(ui);
      const runRow = ui.locator(".work-history > .item").first();
      await runRow.click();
      await expect(ui.getByLabel("Message agent")).toHaveValue(
        "Run-specific message draft",
      );
      await expect(ui.locator(".item-list")).toHaveCount(0);
      await ui.getByRole("button", { name: "← Issues", exact: true }).click();
      await expect(ui.getByLabel("Search inbox")).toHaveValue("Item");
      await expect(ui.getByLabel("Message agent")).toHaveCount(0);
      expect(
        calls.filter((c) =>
          [
            "prepare_launch",
            "start_run",
            "send_message",
            "set_relationship",
          ].includes(c.name),
        ),
      ).toHaveLength(0);
    });
  }
}

for (const theme of ["light", "dark"] as const) {
  test(`small inbox GitHub links open the original issue and PR without selecting either in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    snapshot.inbox[0].item.id = "dkropachev/code-factory-pilot-20261019#1";
    snapshot.inbox[0].item.url =
      "https://github.com/dkropachev/code-factory-pilot-20261019/issues/1";
    const pr = structuredClone(snapshot.inbox[0]);
    pr.item.id = "dkropachev/code-factory-pilot-20261019#2";
    pr.item.kind = "pr";
    pr.item.title = "Original pull request";
    pr.item.url =
      "https://github.com/dkropachev/code-factory-pilot-20261019/pull/2";
    snapshot.inbox.push(pr);
    const { ui, state, calls, openedLinks } = await host(page, {
      snapshot,
      theme,
    });
    const issue = ui.getByRole("link", {
      name: "Open issue dkropachev/code-factory-pilot-20261019#1 on GitHub",
      exact: true,
    });
    const pull = ui.getByRole("link", {
      name: "Open pull request dkropachev/code-factory-pilot-20261019#2 on GitHub",
      exact: true,
    });
    await expect(issue).toHaveText("dkropachev/code-factory-pilot-20261019#1");

    await expect(
      ui.getByText("dkropachev/code-factory-pilot-20261019#1", { exact: true }),
    ).toHaveCount(1);
    await expect(ui.getByText("GitHub", { exact: true })).toHaveCount(0);
    await expect(issue.locator("..")).toHaveClass("item-meta");
    await expect(issue).toHaveAttribute("href", snapshot.inbox[0].item.url);

    await expect(issue).toHaveAttribute("rel", "noopener noreferrer");
    await expect(ui.locator("button a")).toHaveCount(0);
    await issue.click();
    await expect.poll(() => openedLinks.length).toBe(1);
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await expect(pull).toHaveText("dkropachev/code-factory-pilot-20261019#2");
    await expect(pull).toHaveAttribute("href", pr.item.url);
    await ui.getByRole("button", { name: /Original pull request/ }).focus();
    await page.keyboard.press("Tab");
    await expect(pull).toBeFocused();
    await page.keyboard.press("Enter");
    await expect.poll(() => openedLinks.length).toBe(2);
    expect(openedLinks).toEqual([snapshot.inbox[0].item.url, pr.item.url]);
    expect(state.inbox.every((e) => e.unread && e.user.new)).toBe(true);
    expect(
      calls.filter((c) =>
        ["read_item", "item_action", "prepare_launch", "start_run"].includes(
          c.name,
        ),
      ),
    ).toHaveLength(0);
    await expect(ui.locator(".detail")).toHaveCount(0);
    await expect(ui.getByLabel("Search inbox")).toBeVisible();
    expect(
      await ui.locator("body").evaluate((b) => b.scrollWidth <= b.clientWidth),
    ).toBe(true);
    await page.screenshot({
      path: test.info().outputPath(`github-links-${theme}.png`),
    });
    await scanOwnedApp(page, `github-links-${theme}`);
    await ui.getByRole("button", { name: /Original pull request/ }).click();
    await expect(
      ui.getByRole("heading", { name: pr.item.title, level: 1 }),
    ).toBeVisible();
    await expect(ui.locator(".item-list")).toHaveCount(0);
  });
}

for (const failure of ["deny", "rpc"] as const) {
  test(`inbox GitHub link ${failure} failure stays in the list with visible feedback`, async ({
    page,
  }) => {
    const { ui, calls, openedLinks } = await host(page, {
      denyOpenLink: failure === "deny",
      failOpenLink: failure === "rpc",
    });
    await ui
      .getByRole("link", {
        name: "Open issue owner/repo#1 on GitHub",
        exact: true,
      })
      .click();
    await expect(ui.getByRole("alert")).toContainText(
      failure === "deny"
        ? "The host could not open GitHub."
        : "Host link unavailable",
    );
    expect(openedLinks).toHaveLength(1);
    await expect(ui.getByLabel("Search inbox")).toBeVisible();
    expect(
      calls.filter((c) => c.name === "read_item" || c.name === "item_action"),
    ).toHaveLength(0);
  });
}

test("transient item read failure retries and marks only the successfully displayed revision read", async ({
  page,
}) => {
  let itemUnavailable = true;
  const { ui, calls } = await host(page, {
    transportFault: (name) => itemUnavailable && name === "read_item",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("status").filter({ hasText: "Retrying item" }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "item_action")).toHaveLength(0);
  itemUnavailable = false;
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", exact: true }),
  ).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  expect(calls.filter((c) => c.name === "read_item").length).toBeGreaterThanOrEqual(4);
  await expect
    .poll(
      () =>
        calls.filter(
          (c) => c.name === "item_action" && c.arguments.action === "read",
        ).length,
    )
    .toBe(1);
  await expect(ui.getByRole("alert")).toHaveCount(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`Working and linked PR monitoring follow server projections in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const state = initial();
    state.inbox[0].queues = ["issues", "working"];
    state.inbox[0].work = {
      tracked: true,
      complete: false,
      status: "Waiting for you",
      runIds: ["older", "newer"],
      attentionCount: 1,
    };
    const pr = {
      id: "owner/repo#9",
      title: "Reconnect fix",
      url: "https://github.com/owner/repo/pull/9",
      state: "open",
      draft: false,
      head: "current",
      reviewDecision: "approved",
      ci: "pending",
      checks: [
        {
          name: "build",
          status: "in_progress",
          conclusion: "",
          url: "https://github.com/owner/repo/actions/runs/9",
        },
      ],
      syncedAt: new Date().toISOString(),
      implementation: true,
    };
    state.inbox[0].linkedPullRequests = [pr];
    state.runs = [
      retained(state, "older", "waiting"),
      retained(state, "newer", "idle", "2026-10-22T00:00:00Z"),
    ];
    const other = structuredClone(state.inbox[0]);
    other.item.id = "owner/repo#2";
    other.item.title = "Not started";
    other.queues = ["issues"];
    other.work = {
      tracked: false,
      complete: false,
      status: "Not started",
      runIds: [],
      attentionCount: 0,
    };
    other.linkedPullRequests = [];
    state.inbox.push(other);
    const { ui, calls, openedLinks } = await host(page, {
      snapshot: state,
      theme,
    });
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(
      ui.getByRole("region", { name: "Items" }).getByRole("button"),
    ).toHaveCount(2);
    await ui.getByRole("button", { name: "Working", exact: true }).click();
    await expect(
      ui.getByRole("region", { name: "Items" }).getByRole("button"),
    ).toHaveCount(1);
    await expect(ui.getByLabel("Work status")).toHaveText("Waiting for you");
    await expect(ui.getByLabel("Pull request status")).toContainText(
      "open · approved · CI pending",
    );
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await expect(ui.getByLabel("Session status")).toContainText("idle");
    await expect(ui.getByLabel("Work status")).toHaveText("Waiting for you");
    await showItemDetails(ui);
    await ui.getByText("Work history", { exact: true }).click();
    await expect(
      ui.locator(".work-history").getByRole("button", { name: /waiting$/ }),
    ).toBeVisible();
    await expect(
      ui.locator(".work-history").getByRole("button", { name: /idle$/ }),
    ).toBeVisible();
    const linked = ui.getByRole("region", { name: "Linked pull requests" });
    await expect(linked).toContainText("Reconnect fix");
    await linked.getByRole("link", { name: "owner/repo#9" }).click();
    await expect.poll(() => openedLinks).toEqual([pr.url]);
    await linked.getByText("CI checks", { exact: true }).click();
    await linked.getByRole("link", { name: "build", exact: true }).click();
    await expect.poll(() => openedLinks).toEqual([pr.url, pr.checks[0].url]);
    pr.state = "merged";
    pr.ci = "success";
    state.inbox[0].queues = ["issues"];
    state.inbox[0].work!.complete = true;
    state.cursor++;
    await expect(linked).toContainText("merged · approved · CI success");
    await ui.getByRole("button", { name: "← Working", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Fix reconnect regression/ }),
    ).toHaveCount(0);
    expect(
      calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name)),
    ).toHaveLength(0);
    await scanOwnedApp(page, `working-monitoring-${theme}`);
  });
}

test("text fallback opens the same session console and preserves its reply draft", async ({
  page,
}) => {
  const state = initial();
  const run = retained(state, "retained", "interrupted");
  state.runs = [run];
  state.attention = [
    {
      id: "text-question",
      runId: run.id,
      origin: "text",
      method: "item/tool/requestUserInput",
      blocking: true,
      status: "pending",
      params: {
        questions: [
          {
            id: "reply",
            header: "Reply",
            question: "Which target should I use?",
            isOther: true,
          },
        ],
      },
    },
  ];
  const transcript: JournalEvent[] = [
    {
      seq: 1,
      kind: "run.output",
      entity: run.id,
      at: new Date().toISOString(),
      data: {
        method: "item/completed",
        params: {
          item: {
            id: "cmd",
            type: "commandExecution",
            command: "git status",
            aggregatedOutput: "fixture command output",
          },
        },
      },
    },
    {
      seq: 2,
      kind: "run.output",
      entity: run.id,
      at: new Date().toISOString(),
      data: {
        method: "item/completed",
        params: {
          codeFactorySource: "question-recovery",
          item: {
            id: "automatic-input",
            type: "userMessage",
            content: [
              {
                type: "text",
                text: "Internal question form repair instructions",
              },
            ],
          },
        },
      },
    },
  ];
  const { ui, calls } = await host(page, { snapshot: state, transcript });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await ui
    .getByRole("region", { name: "Waiting for you" })
    .getByRole("button")
    .click();
  await ui
    .getByRole("button", { name: "Open session console", exact: true })
    .click();
  await expect(ui.getByLabel("Session console")).toContainText(
    "fixture command output",
  );
  await expect(ui.getByLabel("Session console")).toContainText(
    "Question recovery",
  );
  await expect(ui.getByLabel("Session console")).toContainText(
    "Internal question form repair instructions",
  );
  await ui.getByLabel("Message agent").fill("Console draft");
  await ui
    .getByRole("button", { name: "View conversation", exact: true })
    .click();
  await expect(ui.getByLabel("Message agent")).toHaveValue("Console draft");
  await expect(
    ui.getByText("fixture command output", { exact: false }),
  ).not.toBeVisible();
  await expect(
    ui.getByText("Internal question form repair instructions", { exact: true }),
  ).not.toBeVisible();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await ui
    .getByRole("dialog")
    .getByLabel("Which target should I use?")
    .fill("Target one");
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "answer_request").length)
    .toBe(1);
  expect(
    calls.filter((c) => c.name === "answer_request").map((c) => c.arguments),
  ).toEqual([
    {
      id: "text-question",
      answer: { answers: { reply: { answers: ["Target one"] } } },
    },
  ]);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`review queues from multiple repositories load full details from compact snapshots in ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    snapshot.inbox = ["owner/first", "owner/second"].map((repository, index) => {
      const entry = structuredClone(snapshot.inbox[0]);
      entry.item = {
        ...entry.item,
        id: `${repository}#${index + 1}`,
        repository,
        number: index + 1,
        kind: "pr",
        title: `Review from repository ${index + 1}`,
        author: "other",
        head: `head-${index + 1}`,
        reviewRequests: ["alex"],
        activity: [],
      };
      entry.queues = ["review"];
      return entry;
    });
    const { ui, calls } = await host(page, {
      snapshot,
      theme,
      readItem: async (detail) => ({
        ...detail,
        item: {
          ...detail.item,
          activity: [{ id: "comment:1", kind: "comment", author: "reviewer", body: "Full history is available on demand.", url: "" }],
          checks: { result: "Full checks retained" },
          pr: { id:detail.item.id,title:detail.item.title,url:detail.item.url,state:"open",draft:false,head:detail.item.head,reviewDecision:"none",ci:"success",syncedAt:new Date().toISOString(),checks:[{name:"Full current-head check",status:"completed",conclusion:"success",url:""}] },
        },
      }),
    });
    await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
    await expect(ui.getByRole("region", { name: "Items" }).getByRole("button")).toHaveCount(2);
    expect(calls.filter((c) => c.name === "read_item")).toHaveLength(0);
    await ui.getByRole("button", { name: /Review from repository 2/ }).click();
    await expect(ui.getByRole("heading", { name: "Review from repository 2" })).toBeVisible();
    await ui.getByRole("region",{name:"PR monitoring",exact:true}).getByText("CI checks",{exact:true}).click();
    await expect(ui.getByText(/Full current-head check/)).toBeVisible();
    await ui.getByText("Item details", { exact: true }).click();
    await ui.getByText("Activity", { exact: true }).click();
    await expect(ui.getByText("Full history is available on demand.", { exact: true })).toBeVisible();
    await ui.getByText("Checks", { exact: true }).click();
    await expect(ui.getByText(/Full checks retained/)).toBeVisible();
    expect(calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name))).toHaveLength(0);
    expect(calls.filter((c) => c.name === "read_item").every((c) => c.arguments.id === "owner/second#2")).toBe(true);
  });
}

test("a deferred detail refresh retains saved information and unread state until a successful retry",async({page})=>{
  let deferred=true;
  const {ui,calls}=await host(page,{readItem:async detail=>({...detail,refreshError:deferred?"Could not refresh details; showing saved information. GitHub request reserve reached.":""})});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.getByRole("alert")).toContainText("showing saved information");
  await expect(ui.getByRole("heading",{name:"Fix reconnect regression",exact:true})).toBeVisible();
  expect(calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read")).toHaveLength(0);
  deferred=false;await ui.getByRole("button",{name:"Retry detail refresh",exact:true}).click();
  await expect(ui.getByRole("alert")).toHaveCount(0);
  await expect.poll(()=>calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read").length).toBe(1);
  expect(calls.some(c=>["prepare_launch","start_run"].includes(c.name))).toBe(false);
});

test("cooldown without an error keeps a retry action and saved details unread until fresh", async ({ page }) => {
  let coolingDown = true;
  const retryAt = "2100-06-01T12:00:00Z";
  const { ui, calls, state } = await host(page, {
    readItem: async detail => ({
      ...detail,
      observation: {
        observedAt: "2026-10-24T01:00:00Z",
        refresh: coolingDown ? "cooldown" : "fresh",
        ...(coolingDown ? { retryAt } : {}),
      },
    }),
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByRole("heading", { name: "Fix reconnect regression", exact: true })).toBeVisible();
  const deferred = ui.getByRole("status").filter({ hasText: "Refresh deferred; showing saved information." });
  await expect(deferred).toContainText("Retry after");
  await expect(deferred).toContainText("2100");
  const draft = ui.getByRole("textbox", { name: "Message agent", exact: true });
  await draft.fill("Keep my retry draft.");
  const retry = ui.getByRole("button", { name: "Retry detail refresh", exact: true });
  await retry.click();
  await expect.poll(() => calls.filter(c => c.name === "read_item").length).toBe(2);
  await expect(deferred).toBeVisible();
  expect(state.inbox[0].unread).toBe(true);
  expect(calls.filter(c => c.name === "item_action" && c.arguments.action === "read")).toHaveLength(0);
  await expect(draft).toHaveValue("Keep my retry draft.");
  coolingDown = false;
  await retry.click();
  await expect(deferred).toHaveCount(0);
  await expect(retry).toHaveCount(0);
  await expect.poll(() => calls.filter(c => c.name === "item_action" && c.arguments.action === "read").length).toBe(1);
  expect(state.inbox[0].unread).toBe(false);
  await expect(draft).toHaveValue("Keep my retry draft.");
  expect(calls.filter(c => ["prepare_launch", "prepare_pr_review", "start_run"].includes(c.name))).toHaveLength(0);
});

test("fresh read markers observe external changes after a host memoizes an empty journal page", async ({ page }) => {
  const { ui, calls, state } = await host(page, { memoizeReads: true });
  await expect.poll(() => calls.filter(c => c.name === "watch_ui_changes").length).toBeGreaterThan(0);
  state.inbox[0].item.title = "Externally updated pilot review";
  state.cursor++;
  await expect(ui.getByRole("button", { name: /Externally updated pilot review/ })).toBeVisible();
  state.inbox[0].item.title = "Second external review update";
  state.cursor++;
  await expect(ui.getByRole("button", { name: /Second external review update/ })).toBeVisible();
  const reads = calls.filter(c => ["watch_ui_changes", "read_ui_state"].includes(c.name));
  expect(new Set(reads.map(c => c.arguments.freshness)).size).toBe(reads.length);
  expect(reads.every(c => typeof c.arguments.freshness === "string" && c.arguments.freshness.length > 0)).toBe(true);
  expect(calls.some(c => ["prepare_launch", "start_run", "send_message"].includes(c.name))).toBe(false);
});

for (const theme of ["light", "dark"] as const) {
  test(`posted reviews show verified verdicts findings and refresh after completion in ${theme}`, async ({page}) => {
    await page.setViewportSize({width:390,height:820});
    const snapshot=initial();const entry=snapshot.inbox[0];
    Object.assign(entry.item,{kind:"pr",head:"current-head",author:"someone",reviewRequests:["alex"]});entry.queues=["review"];
    let response: PRReviews={itemId:entry.item.id,reviews:[],syncedAt:"0001-01-01T00:00:00Z",lastAttempt:"0001-01-01T00:00:00Z",error:"GitHub temporarily unavailable"};
    const {ui,calls,state,openedLinks}=await host(page,{snapshot,theme,readReviews:async()=>structuredClone(response)});
    await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
    await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
    const reviews=ui.getByRole("region",{name:"Posted reviews",exact:true});
    await expect(reviews.getByText("Publication has not been verified.",{exact:true})).toBeVisible();
    await expect(reviews.getByText("GitHub temporarily unavailable",{exact:true})).toBeVisible();
    await expect(reviews.getByText(/No posted reviews found/)).toHaveCount(0);
    response={itemId:entry.item.id,syncedAt:new Date().toISOString(),lastAttempt:new Date().toISOString(),reviews:[
      {id:"9",url:"https://github.com/owner/repo/pull/1#pullrequestreview-9",author:"alex",state:"COMMENTED",head:"current-head",submittedAt:"2026-10-24T03:00:00Z",body:"Concrete verified review",comments:[{id:"10",url:"https://github.com/owner/repo/pull/1#discussion_r10",author:"alex",path:"lib/reactor.py",line:79,body:"Actionable P2 failure"}]},
      {id:"8",url:"https://github.com/owner/repo/pull/1#pullrequestreview-8",author:"other",state:"APPROVED",head:"earlier-head",submittedAt:"2026-10-23T03:00:00Z",body:"Earlier approval",comments:[]}
    ]};
    state.reviewVersions={[entry.item.id]:response.lastAttempt};state.cursor++;
    await expect(reviews.getByText("Concrete verified review",{exact:true})).toBeVisible();
    await expect(reviews.getByText("Actionable P2 failure",{exact:true})).toBeVisible();
    await expect(reviews).toContainText("COMMENTED");await expect(reviews).toContainText("APPROVED");
    await expect(reviews).toContainText("Current head");await expect(reviews).toContainText("Earlier head");
    await reviews.getByRole("link",{name:"Review by alex",exact:true}).click();
    await reviews.getByRole("link",{name:"lib/reactor.py:79",exact:true}).click();
    await expect.poll(()=>openedLinks).toEqual([response.reviews[0].url,response.reviews[0].comments[0].url]);
    response={...response,error:"Could not refresh posted reviews; showing saved information."};
    await reviews.getByRole("button",{name:"Refresh reviews",exact:true}).click();
    await expect(reviews.getByRole("alert")).toContainText("showing saved information");
    await expect(reviews.getByText("Concrete verified review",{exact:true})).toBeVisible();
    expect(calls.filter(c=>["start_run","prepare_launch","prepare_pr_review"].includes(c.name))).toHaveLength(0);
    expect(calls.filter(c=>c.name==="read_pr_reviews").every(c=>c.arguments.id===entry.item.id)).toBe(true);
    await scanOwnedApp(page,`${theme}-posted-reviews`);
  });
}

for (const theme of ["light", "dark"] as const) {
  test(`saved details and receipts render during background refresh in ${theme}`, async ({page}) => {
    const snapshot=initial();const entry=snapshot.inbox[0];
    Object.assign(entry.item,{kind:"pr",head:"current-head",author:"someone",reviewRequests:["alex"]});entry.queues=["review"];
    let pending=true;
    const {ui,calls}=await host(page,{snapshot,theme,
      readItem:async(detail)=>({...detail,observation:{observedAt:"2026-10-24T01:00:00Z",refresh:pending?"pending":"fresh"}}),
      readReviews:async()=>({itemId:entry.item.id,refresh:pending?"pending":"fresh",syncedAt:"2026-10-24T01:00:00Z",lastAttempt:"2026-10-24T01:00:00Z",reviews:[{id:"saved",url:"https://github.com/owner/repo/pull/1#pullrequestreview-1",author:"alex",state:"COMMENTED",head:"current-head",submittedAt:"2026-10-24T01:00:00Z",body:"Saved receipt available immediately",comments:[]}]})
    });
    await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
    await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
    await expect(ui.getByText("Refreshing saved details…",{exact:true})).toBeVisible();
    await expect(ui.getByText("Refreshing saved reviews…",{exact:true})).toBeVisible();
    await expect(ui.getByText("Saved receipt available immediately",{exact:true})).toBeVisible();
    expect(calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read")).toHaveLength(0);
    pending=false;
    snapshot.cursor++;
    await expect(ui.getByText("Refreshing saved details…",{exact:true})).toHaveCount(0);
    await expect(ui.getByText("Refreshing saved reviews…",{exact:true})).toHaveCount(0);
    await expect.poll(()=>calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read").length).toBe(1);
    expect(calls.filter(c=>["start_run","prepare_launch","prepare_pr_review"].includes(c.name))).toHaveLength(0);
    expect(new Set(calls.filter(c=>c.name==="read_pr_reviews").map(c=>c.arguments.freshness)).size).toBeGreaterThan(1);
    await scanOwnedApp(page,`${theme}-saved-observations`);
  });
}

for (const refresh of ["paused", "cooldown", "unavailable"] as const) {
  test(`saved unread details remain unread when refresh is ${refresh} without an error`, async ({ page }) => {
    const { ui, calls, state } = await host(page, {
      readItem: async (detail) => ({ ...detail, observation: { observedAt: "2026-10-24T01:00:00Z", refresh } }),
    });
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await expect(ui.getByRole("heading", { name: "Fix reconnect regression", exact: true })).toBeVisible();
    await ui.getByLabel("Agent pipeline", { exact: true }).selectOption("implement");
    await ui.getByRole("textbox", { name: "Message agent", exact: true }).fill("Keep the saved conversation draft.");
    expect(state.inbox[0].unread).toBe(true);
    expect(calls.filter((c) => c.name === "item_action" && c.arguments.action === "read")).toHaveLength(0);
    expect(calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name))).toHaveLength(0);
  });
}

test("published inbox responses preserve the newer query when older reads finish later",async({page})=>{
  const snapshot=initial();
  const second=structuredClone(snapshot.inbox[0]);
  second.item.id="owner/repo#2";second.item.title="Second matching item";
  snapshot.inbox.push(second);
  let release:()=>void=()=>{};
  let delayed=false;
  let staleReturned=false;
  const {ui,calls}=await host(page,{snapshot,readUIState:async(state,args)=>{
    if(args.search==="Fix") { delayed=true;await new Promise<void>(resolve=>{release=resolve});staleReturned=true; }
    return state;
  }});
  await ui.getByLabel("Search inbox").fill("Fix");
  await expect.poll(()=>delayed).toBe(true);
  await ui.getByLabel("Search inbox").fill("Second");
  await expect(ui.getByRole("button",{name:/Second matching item/})).toBeVisible();
  const reads=calls.filter(c=>c.name==="read_ui_state").length;
  release();
  await expect.poll(()=>staleReturned).toBe(true);
  await ui.locator("body").evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
  await expect(ui.getByLabel("Search inbox")).toHaveValue("Second");
  await expect(ui.locator(".inbox-row")).toHaveCount(1);
  await expect(ui.getByRole("button",{name:/Fix reconnect regression/})).toHaveCount(0);
  expect(calls.filter(c=>["read_snapshot","read_changes"].includes(c.name))).toHaveLength(0);
  expect(reads).toBeGreaterThanOrEqual(3);
});

test("hidden views catch up through the shared subscription without rereading panels while hidden",async({page})=>{
  const {ui,state,calls}=await host(page);
  await expect(ui.getByRole("button",{name:/Fix reconnect regression/})).toBeVisible();
  await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
    Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>true});
    frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
  });
  const reads=calls.filter(c=>c.name==="read_ui_state").length;
  const watches=calls.filter(c=>c.name==="watch_ui_changes").length;
  state.inbox[0].item.title="Committed while hidden";state.cursor++;
  await expect.poll(()=>calls.filter(c=>c.name==="watch_ui_changes").length).toBeGreaterThan(watches);
  expect(calls.filter(c=>c.name==="read_ui_state")).toHaveLength(reads);
  await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
    Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>false});
    frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
  });
  await expect(ui.getByRole("button",{name:/Committed while hidden/})).toBeVisible();
});

test("inbox pagination and complete-dataset search keep later saved items reachable",async({page})=>{
  const snapshot=initial();
  snapshot.inbox=Array.from({length:125},(_,n)=>{
    const entry=structuredClone(initial().inbox[0]);
    entry.item.id="owner/repo#"+(n+1);entry.item.title="Saved item "+String(n+1).padStart(3,"0");entry.item.updated="2026-10-27T12:00:00Z";return entry;
  });
  const {ui}=await host(page,{snapshot});
  await expect(ui.locator(".inbox-row")).toHaveCount(50);
  await ui.getByRole("button",{name:"Next items"}).click();
  await expect(ui.getByRole("button",{name:/Saved item 051/})).toBeVisible();
  await ui.getByRole("button",{name:"Next items"}).click();
  await expect(ui.locator(".inbox-row")).toHaveCount(25);
  await ui.getByRole("button",{name:/Saved item 125/}).click();
  await expect(ui.getByRole("heading",{name:"Saved item 125",exact:true})).toBeVisible();
  await ui.getByRole("button",{name:"← Issues",exact:true}).click();
  await expect(ui.locator(".inbox-row")).toHaveCount(25);
  await expect(ui.getByRole("button",{name:/Saved item 125/})).toBeFocused();
  await ui.getByLabel("Search inbox").fill("Saved item 125");
  await expect(ui.locator(".inbox-row")).toHaveCount(1);
  await ui.getByRole("button",{name:/Saved item 125/}).click();
  await expect(ui.getByRole("heading",{name:"Saved item 125",exact:true})).toBeVisible();
});

test("a selected item outside the current page never renders an earlier selected entry",async({page})=>{
  const snapshot=initial();
  const second=structuredClone(snapshot.inbox[0]);
  second.item.id="owner/repo#60";second.item.title="Later page question";
  snapshot.inbox.push(second);
  const run=retained(snapshot,"later-page","waiting");run.itemId=second.item.id;
  snapshot.runs=[run];
  snapshot.attention=[{id:"later-request",runId:run.id,status:"pending",method:"item/tool/requestUserInput",blocking:true,params:{questions:[{id:"scope",question:"Which scope?",options:[{label:"Focused"},{label:"Broad"}]}]}} as Snapshot["attention"][number]];
  let release:()=>void=()=>{};
  let pending=false;
  const blocked=new Promise<void>(resolve=>{release=resolve});
  const {ui}=await host(page,{snapshot,readUIState:async(data,args)=>{
    if(args.id===second.item.id){pending=true;await blocked;}
    // Emulate a page that excludes the question's item; selection is independent.
    if(data.inbox)data.inbox=data.inbox.filter(e=>e.item.id!==second.item.id);
    return data;
  }});
  try{
    await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
    await expect(ui.getByRole("heading",{name:"Fix reconnect regression",exact:true})).toBeVisible();
    await ui.getByRole("button",{name:"Messages 1",exact:true}).click();
    await ui.getByRole("region",{name:"Waiting for you"}).getByRole("button").click();
    await expect.poll(()=>pending).toBe(true);
    await expect(ui.locator(".detail h1").filter({hasText:"Fix reconnect regression"})).toHaveCount(0);
    release();
    await expect(ui.locator(".detail h1").filter({hasText:"Later page question"})).toBeVisible();
  }finally{release()}
});

test("observation-only publications refresh the selected saved error without a broad read",async({page})=>{
  let failed=false;
  const {ui,state,calls}=await host(page,{watchDomains:["observation"],readItem:async detail=>({...detail,refreshError:failed?"Background provider failed":"",observation:{observedAt:"2026-10-27T12:00:00Z",refresh:failed?"cooldown":"fresh"}})});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.getByRole("heading",{name:"Fix reconnect regression",exact:true})).toBeVisible();
  const reads=calls.filter(c=>c.name==="read_ui_state").length;
  failed=true;state.cursor++;
  await expect(ui.getByRole("alert")).toContainText("Background provider failed");
  expect(calls.filter(c=>c.name==="read_ui_state")).toHaveLength(reads);
});

test("repository-only resume refreshes selected detail and posted-review freshness",async({page})=>{
  const snapshot=initial();snapshot.profiles[0].id="review";
  const entry=snapshot.inbox[0];
  Object.assign(entry.item,{kind:"pr",author:"someone",reviewRequests:["alex"]});
  entry.queues=["review"];
  snapshot.repositories=[{id:"owner/repo",paused:true,checkout:"",base:""} as Snapshot["repositories"][number]];
  const {ui,state,calls}=await host(page,{snapshot,watchDomains:["repository"],
    readItem:async detail=>({...detail,observation:{observedAt:"2026-10-27T12:00:00Z",refresh:snapshot.repositories[0].paused?"paused":"fresh"}}),
    readReviews:async()=>({itemId:entry.item.id,refresh:snapshot.repositories[0].paused?"paused":"fresh",syncedAt:"2026-10-27T12:00:00Z",lastAttempt:"2026-10-27T12:00:00Z",reviews:[]})});
  await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.getByText("Repository monitoring is paused.",{exact:true})).toBeVisible();
  await ui.getByLabel("Message agent").fill("Keep my draft when monitoring resumes.");
  const details=calls.filter(c=>c.name==="read_item").length;
  const reviews=calls.filter(c=>c.name==="read_pr_reviews").length;
  state.repositories[0].paused=false;state.cursor++;
  await expect(ui.getByText("Repository monitoring is paused.",{exact:true})).toHaveCount(0);
  await expect.poll(()=>calls.filter(c=>c.name==="read_item").length).toBeGreaterThan(details);
  expect(calls.filter(c=>c.name==="read_pr_reviews").length).toBeGreaterThan(reviews);
  await expect(ui.getByLabel("Message agent")).toHaveValue("Keep my draft when monitoring resumes.");
  expect(calls.filter(c=>["sync_repositories","start_run"].includes(c.name))).toHaveLength(0);
});

test("a hidden full resync rejects a delayed reply from the previous state epoch",async({page})=>{
  const snapshot=initial();snapshot.cursor=100;
  let release:()=>void=()=>{};
  const gate=new Promise<void>(resolve=>{release=resolve});
  let captured=false,returned=false;
  const {ui,state,calls}=await host(page,{snapshot,readUIState:async(data,args)=>{
    if(args.search==="delayed"&&!captured){captured=true;await gate;returned=true}
    return data;
  }});
  try{
    await ui.getByRole("button",{name:/Fix reconnect regression/}).waitFor();
    await ui.getByLabel("Search inbox").fill("delayed");
    await expect.poll(()=>captured).toBe(true);
    await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
      Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>true});
      frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
    });
    state.inbox[0].item.title="Restored after hidden resync";state.cursor=3;
    await expect.poll(()=>calls.some(c=>c.name==="watch_ui_changes"&&c.arguments.after===3)).toBe(true);
    release();
    await expect.poll(()=>returned).toBe(true);
    await ui.locator("body").evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
    await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
      Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>false});
      frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
    });
    await ui.getByLabel("Search inbox").fill("");
    await expect(ui.getByRole("button",{name:/Restored after hidden resync/})).toBeVisible();
  }finally{release()}
});

test("failed requested view reads recover without a new journal publication",async({page})=>{
  let unavailable=false;
  const snapshot=initial();snapshot.inbox[0].item.title="Retry target";
  const {ui,calls}=await host(page,{snapshot,transportFault:(name,args)=>name==="read_ui_state"&&args.search==="Retry target"&&unavailable});
  await ui.getByRole("button",{name:/Retry target/}).waitFor();
  unavailable=true;
  await ui.getByLabel("Search inbox").fill("Retry target");
  await expect(ui.getByRole("alert").filter({hasText:"Retrying saved view"})).toBeVisible();
  const watches=calls.filter(c=>c.name==="watch_ui_changes").length;
  await expect.poll(()=>calls.filter(c=>c.name==="watch_ui_changes").length).toBeGreaterThan(watches);
  await expect(ui.getByRole("alert").filter({hasText:"Retrying saved view"})).toBeVisible();
  unavailable=false;
  await expect(ui.getByRole("button",{name:/Retry target/})).toBeVisible();
  await expect(ui.getByRole("alert")).toHaveCount(0);
  expect(snapshot.cursor).toBe(1);
});

test("queue navigation cannot activate rows belonging to the previous query",async({page})=>{
  const snapshot=initial();
  const pr=structuredClone(snapshot.inbox[0]);Object.assign(pr.item,{id:"owner/repo#2",kind:"pr",title:"Reviewable pull request",author:"someone",reviewRequests:["alex"]});pr.queues=["review"];
  snapshot.inbox.push(pr);
  let release:()=>void=()=>{};
  const gate=new Promise<void>(resolve=>{release=resolve});let pending=false;
  const {ui,calls}=await host(page,{snapshot,readUIState:async(data,args)=>{if(args.queue==="review"){pending=true;await gate}return data}});
  try{
    await ui.getByRole("button",{name:/Fix reconnect regression/}).waitFor();
    await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
    await expect.poll(()=>pending).toBe(true);
    await expect(ui.getByRole("button",{name:/Fix reconnect regression/})).toHaveCount(0);
    await expect(ui.getByText("Loading saved items…",{exact:true})).toBeVisible();
    release();
    await expect(ui.getByRole("button",{name:/Reviewable pull request/})).toBeVisible();
    expect(calls.filter(c=>["prepare_launch","start_run"].includes(c.name))).toHaveLength(0);
  }finally{release()}
});

test("observation-only completion refreshes posted review monitoring state",async({page})=>{
  const snapshot=initial();snapshot.profiles[0].id="review";
  const entry=snapshot.inbox[0];Object.assign(entry.item,{kind:"pr",author:"someone",reviewRequests:["alex"]});entry.queues=["review"];
  let pending=true;
  const {ui,state}=await host(page,{snapshot,watchDomains:["observation"],readReviews:async()=>({itemId:entry.item.id,refresh:pending?"pending":"fresh",syncedAt:"2026-10-27T12:00:00Z",lastAttempt:"2026-10-27T12:00:00Z",reviews:[]})});
  await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.getByText("Refreshing saved reviews…",{exact:true})).toBeVisible();
  pending=false;state.cursor++;
  await expect(ui.getByText("Refreshing saved reviews…",{exact:true})).toHaveCount(0);
});

for(const modal of [false,true]){
  test("a delayed "+(modal?"modal":"first-message")+" Start response cannot cross a full resync epoch",async({page})=>{
    const snapshot=initial();snapshot.cursor=100;
    let release:()=>void=()=>{};
    const gate=new Promise<void>(resolve=>{release=resolve});
    const {ui,state,calls}=await host(page,{snapshot,beforeStartResponse:()=>{snapshot.runs[0].status="prepared";return gate}});
    try{
      await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
      if(modal){await showItemDetails(ui);await ui.getByRole("button",{name:"New run",exact:true}).click()}
      await ui.getByRole(modal?"dialog":"region",{name:modal?"Launch agent":"New agent conversation"}).getByRole("button",{name:"Start agent",exact:true}).click();
      await expect.poll(()=>calls.filter(c=>c.name==="start_run").length).toBe(1);
      state.cursor=3;
      await expect.poll(()=>calls.some(c=>c.name==="watch_ui_changes"&&c.arguments.after===3)).toBe(true);
      release();
      await expect(ui.getByRole("alert").filter({hasText:"Saved state changed during the request"})).toBeVisible();
      await expect(ui.getByRole("button",{name:"Messages",exact:true})).toBeVisible();
      expect(calls.filter(c=>c.name==="start_run")).toHaveLength(1);
    }finally{release()}
  });
}

for (const delay of ["import", "snapshot"] as const) {
  test(`repository DK import discards stale results when switching editors during ${delay}`, async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 820 });
    const snapshot = initial();
    const repo = (id: string) => ({ id, checkout: "", base: "main", lastSync: "", error: "", paused: false,
      reviewers: [{ id, kind: "skill" as const, skillId: "fixture" }], reviewRules: `${id} saved rules` });
    snapshot.repositories = [
      { ...repo("owner/first"), reviewers: undefined, reviewRules: undefined },
      repo("owner/second"),
    ];
    const importedReviewers = [{ id: "imported", kind: "skill" as const, skillId: "fixture" }];
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    let loading = false;
    let completed = false;
    const pause = async () => { loading = true; await gate; completed = true; };
    const { ui, calls, state } = await host(page, {
      snapshot, surface: "settings",
      dkReviewSettings: { reviewers: importedReviewers, reviewRules: "First repository imported rules" },
      beforeDKImport: delay === "import" ? pause : undefined,
      readUIState: delay === "snapshot" ? async (next, args) => {
        if ((args.views as string[]).includes("settings") && next.repositories?.find(repository => repository.id === "owner/first")?.reviewers?.[0]?.id === "imported") await pause();
        return next;
      } : undefined,
    });
    await ui.getByRole("button", { name: "Edit repository owner/first", exact: true }).click();
    const configuration = ui.getByRole("group", { name: "Review workflow configuration" }).first();
    try {
      await ui.getByRole("button", { name: "Import DK review settings once", exact: true }).first().click();
      await expect.poll(() => loading).toBe(true);
      await ui.getByRole("button", { name: "Edit repository owner/second", exact: true }).click();
      const secondReviewers = [{ id: "second-draft", kind: "skill", skillId: "fixture" }];
      await configuration.getByLabel("Ordered reviewers").fill(JSON.stringify(secondReviewers));
      await configuration.getByLabel("Private review rules", { exact: true }).fill("Second repository unsaved rules");
      await ui.getByLabel("Default base").fill("second-draft-base");
      release();
      await expect.poll(() => completed).toBe(true);
      await expect(configuration.getByLabel("Ordered reviewers")).toHaveValue(JSON.stringify(secondReviewers, null, 2));
      await expect(configuration.getByLabel("Private review rules", { exact: true })).toHaveValue("Second repository unsaved rules");
      await expect(ui.getByLabel("Default base")).toHaveValue("second-draft-base");
      await expect(ui.getByRole("status").filter({ hasText: /Imported: reviewers, reviewRules/ })).toHaveCount(0);
      await ui.getByRole("button", { name: "Save repository", exact: true }).click();
      await expect.poll(() => calls.filter((call) => call.name === "save_repository").length).toBe(1);
      expect(state.repositories.find((repository) => repository.id === "owner/second")).toMatchObject({
        reviewers: secondReviewers, reviewRules: "Second repository unsaved rules", base: "second-draft-base",
      });
      expect(state.repositories.find((repository) => repository.id === "owner/first")).toMatchObject({
        reviewers: importedReviewers, reviewRules: "First repository imported rules",
      });
      expect(calls.filter((call) => call.name === "import_dk_review_settings").map((call) => call.arguments.repository)).toEqual(["owner/first"]);
      await expect(ui.locator("body")).toHaveJSProperty("scrollWidth", await ui.locator("body").evaluate((element) => element.clientWidth));
      expect(calls.filter((call) => ["start_run", "post_retained_review"].includes(call.name))).toHaveLength(0);
    } finally { release(); }
  });
}

for (const response of ["success", "failure"] as const) {
  test(`a delayed policy save ${response} preserves a newer committed Off observation`, async ({ page }) => {
    const snapshot = initial();
    Object.assign(snapshot.inbox[0].item, { kind: "pr", head: "a".repeat(40), author: "other", reviewRequests: ["alex"] });
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    let committed = false;
    const { ui, state, calls, prPolicies } = await host(page, {
      snapshot,
      watchDomains: ["pr-review-policy"],
      afterSaveReviewPolicy: async () => {
        committed = true;
        await gate;
        if (response === "failure") throw new Error("Earlier save reply unavailable");
      },
    });
    try {
      await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
      await openItem(ui, /Fix reconnect regression/);
      const policy = ui.getByRole("region", { name: "PR review posting policy" });
      const select = policy.getByLabel("PR automatic review posting");
      await expect(policy).toContainText("Off · Source: global");
      await select.selectOption("true");
      await expect.poll(() => committed).toBe(true);
      prPolicies.set(snapshot.inbox[0].item.id, false);
      state.cursor++;
      await expect(policy).toContainText("Off · Source: pr");
      await expect(select).toHaveValue("false");
      await expect(select).toBeDisabled();
      release();
      await expect(select).toBeEnabled();
      await expect(select).toHaveValue("false");
      await expect(policy).toContainText("Off · Source: pr");
      await expect(policy.getByRole("alert")).toHaveCount(0);
      expect(calls.filter((call) => call.name === "save_review_policy")).toHaveLength(1);
      expect(calls.filter((call) => ["start_run", "post_retained_review"].includes(call.name))).toHaveLength(0);
    } finally { release(); }
  });
}
