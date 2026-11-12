import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { host, initial } from "./wrapper-fixtures";

test("the first accepted daemon retires concurrent initial replies from an obsolete instance", async ({
  page,
}) => {
  let instance = "initial-old";
  let held = false;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls, toolCompletions } = await host(page, {
    waitForConnection: false,
    observeToolCompletions: true,
    instanceId: () => instance,
    before: async (call) => {
      if (
        call.name === "factory_state" &&
        call.arguments.view === "issues" &&
        !held
      ) {
        held = true;
        await gate;
      }
    },
  });
  try {
    await expect.poll(() => held).toBe(true);
    const frame = page.frames().find((frame) => frame.parentFrame())!;
    await frame.evaluate(async () => {
      const bridge = await import("/src/bridge.ts");
      (window as any).initialInstanceChanges = [];
      bridge.onInstanceChange((id: string) =>
        (window as any).initialInstanceChanges.push(id),
      );
    });
    instance = "initial-new";
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Reconnect PR/ }),
    ).toBeVisible();
    expect(
      toolCompletions.filter(
        (call) =>
          call.name === "factory_state" && call.arguments.view === "issues",
      ),
    ).toHaveLength(0);
    release();
    await expect
      .poll(
        () =>
          toolCompletions.filter(
            (call) =>
              call.name === "factory_state" && call.arguments.view === "issues",
          ).length,
      )
      .toBeGreaterThan(0);
    expect(
      await frame.evaluate(() => (window as any).initialInstanceChanges),
    ).not.toContain("initial-old");
    await ui.getByRole("button", { name: /Reconnect PR/ }).click();
    await expect(
      ui.getByRole("button", { name: "Mark unread", exact: true }),
    ).toBeEnabled();
    await ui.getByRole("button", { name: "Mark unread", exact: true }).click();
    await expect
      .poll(() =>
        calls
          .filter(
            (call) =>
              call.name === "user_action" && call.arguments.action === "unread",
          )
          .map((call) => call.arguments.instanceId),
      )
      .toEqual(["initial-new"]);
  } finally {
    release();
  }
});

for (const restartedVersion of [1, 1000]) {
  test(`daemon restart accepts revision ${restartedVersion} without losing drafts`, async ({
    page,
  }) => {
    let instance = "before-restart";
    let version = 1000;
    const { ui, state, calls } = await host(page, {
      instanceId: () => instance,
      stateVersion: () => version,
    });
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect(
      ui.getByText("Retain work through disconnection", { exact: true }),
    ).toBeVisible();
    await ui
      .getByLabel("Message", { exact: true })
      .fill("Keep this across restart");
    state.inbox[0].item.title = "Saved after restart";
    state.inbox[0].item.body = "Current saved source from the new daemon";
    state.inbox[0].item.revision = "after-restart";
    instance = "after-restart";
    version = restartedVersion;
    await expect(
      ui.getByText("Current saved source from the new daemon", { exact: true }),
    ).toBeVisible();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Keep this across restart",
    );
    await expect
      .poll(() =>
        calls
          .filter(
            (c) =>
              c.name === "user_action" &&
              c.arguments.revision === "after-restart",
          )
          .map((c) => c.arguments.instanceId),
      )
      .toEqual(["after-restart"]);
    await ui.getByRole("button", { name: "Back", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Saved after restart/ }),
    ).toBeVisible();
    expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(0);
  });
}

test("an open archive retires its page and selection when the daemon changes", async ({
  page,
}) => {
  let instance = "before-restart";
  let pending = true;
  const archive = {
    events: [] as Record<string, string>[],
    deliveries: [{ id: "event-before", action: "old" }],
  };
  const { ui, calls } = await host(page, {
    instanceId: () => instance,
    archive,
    response: (call) =>
      call.name === "factory_events" && instance === "after-restart" && pending
        ? { state: "pending" }
        : undefined,
  });
  await ui.getByRole("button", { name: "Events", exact: true }).click();
  await ui.getByLabel("Archive kind").selectOption("deliveries");
  await ui.getByLabel("Archive page size").selectOption("100");
  await ui
    .getByRole("button", { name: "event-before old", exact: true })
    .click();
  await expect(ui.getByLabel("Selected archive record")).toContainText(
    "event-before",
  );
  const before = calls.filter((call) => call.name === "factory_events").length;
  archive.deliveries = [{ id: "event-after", action: "current" }];
  instance = "after-restart";
  await expect
    .poll(() => calls.filter((call) => call.name === "factory_events").length)
    .toBeGreaterThan(before);
  await expect(
    ui.getByRole("status").filter({ hasText: "Loading archive page" }),
  ).toBeVisible();
  await expect(ui.getByLabel("Selected archive record")).toHaveCount(0);
  await expect(
    ui.getByRole("button", { name: "event-before old", exact: true }),
  ).toHaveCount(0);
  pending = false;
  await expect(
    ui.getByRole("button", { name: "event-after current", exact: true }),
  ).toBeVisible();
  await expect(ui.getByLabel("Selected archive record")).toHaveCount(0);
  await expect(ui.getByLabel("Archive kind")).toHaveValue("deliveries");
  await expect(ui.getByLabel("Archive page size")).toHaveValue("100");
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await expect(ui.getByRole("button", { name: /Fix reconnect/ })).toBeVisible();
  expect(calls.filter((call) => call.name === "session_chat")).toHaveLength(0);
});

test("a superseded daemon detail cannot display or acknowledge old content", async ({
  page,
}) => {
  let instance = "before-restart";
  let version = 1000;
  let held = false;
  let release!: () => void;
  const barrier = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls, state, toolCompletions } = await host(page, {
    instanceId: () => instance,
    stateVersion: () => version,
    observeToolCompletions: true,
    after: async (call) => {
      if (call.name === "factory_item" && !held) {
        held = true;
        await barrier;
      }
    },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect.poll(() => held).toBe(true);
    state.inbox[0].item.body = "Only this daemon source may be acknowledged";
    state.inbox[0].item.revision = "after-restart";
    instance = "after-restart";
    version = 1;
    await expect(
      ui.getByText("Only this daemon source may be acknowledged", {
        exact: true,
      }),
    ).toBeVisible();
    await expect
      .poll(() =>
        calls.filter(
          (call) =>
            call.name === "user_action" &&
            call.arguments.revision === "after-restart",
        ),
      )
      .toHaveLength(1);
    const completed = toolCompletions.filter(
      (call) => call.name === "factory_item",
    ).length;
    release();
    await expect
      .poll(
        () =>
          toolCompletions.filter((call) => call.name === "factory_item").length,
      )
      .toBeGreaterThan(completed);
    await expect(
      ui.getByText("Only this daemon source may be acknowledged", {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      ui.getByText("Retain work through disconnection", { exact: true }),
    ).not.toBeVisible();
    expect(
      calls.filter(
        (call) =>
          call.name === "user_action" && call.arguments.revision === "v1",
      ),
    ).toHaveLength(0);
    await ui.getByRole("button", { name: "Mark unread", exact: true }).click();
    await expect
      .poll(() =>
        calls
          .filter(
            (call) =>
              call.name === "user_action" && call.arguments.action === "unread",
          )
          .map((call) => call.arguments.instanceId),
      )
      .toEqual(["after-restart"]);
  } finally {
    release();
  }
});

test("a late old-daemon command response resolves its original identity without replay", async ({
  page,
}) => {
  let instance = "before-restart";
  let held = false;
  let release!: () => void;
  const barrier = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls, state } = await host(page, {
    instanceId: () => instance,
    after: async (call) => {
      if (call.name === "session_chat") {
        held = true;
        await barrier;
      }
    },
    response: (call) =>
      call.name === "operation_status"
        ? { state: "uncertain", error: "Restart interrupted delivery" }
        : undefined,
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await ui
      .getByLabel("Message", { exact: true })
      .fill("Inspect this original submission");
    await ui.getByRole("button", { name: "Send", exact: true }).click();
    await expect.poll(() => held).toBe(true);
    state.inbox[0].item.title = "Saved after restart";
    instance = "after-restart";
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Saved after restart/ }),
    ).toBeVisible();
    release();
    await expect(ui.getByRole("alert")).toContainText(
      "Operation uncertain; inspect saved state before retrying.",
    );
    await expect(ui.getByRole("alert")).toContainText(
      "Restart interrupted delivery",
    );
    await ui.getByRole("button", { name: /Saved after restart/ }).click();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Inspect this original submission",
    );
    const submitted = calls.filter((call) => call.name === "session_chat");
    expect(submitted).toHaveLength(1);
    expect(
      calls
        .filter((call) => call.name === "operation_status")
        .every(
          (call) =>
            call.arguments.instanceId === "before-restart" &&
            call.arguments.requestId === submitted[0].arguments.requestId,
        ),
    ).toBe(true);
  } finally {
    release();
  }
});

test("inbox source links open GitHub without selecting or reading an item", async ({
  page,
}) => {
  const { ui, calls, openedLinks, state } = await host(page);
  await ui.getByRole("link", { name: "Open o/r#1 on GitHub" }).click();
  await expect
    .poll(() => openedLinks)
    .toEqual(["https://github.com/o/r/issues/1"]);
  await expect(ui.getByRole("button", { name: /Fix reconnect/ })).toBeVisible();
  expect(state.inbox[0].unread).toBe(true);
  expect(
    calls.some((c) =>
      [
        "factory_item",
        "user_action",
        "session_chat",
        "session_control",
      ].includes(c.name),
    ),
  ).toBe(false);
  await ui.getByRole("button", { name: "My PRs", exact: true }).click();
  await ui.getByRole("link", { name: "Open o/r#2 on GitHub" }).click();
  await expect.poll(() => openedLinks).toHaveLength(2);
  expect(openedLinks[1]).toBe("https://github.com/o/r/pull/2");
  expect(state.inbox[1].unread).toBe(true);
  expect(
    calls.some((c) => ["factory_item", "user_action"].includes(c.name)),
  ).toBe(false);
});

test("opening an issue or PR does not create or flush a session", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect" }),
  ).toBeVisible();
  await expect(ui.getByLabel("Session status")).toContainText("Not started");
  await ui.getByRole("button", { name: "Back", exact: true }).click();
  await ui.getByRole("button", { name: "My PRs", exact: true }).click();
  await ui.getByRole("button", { name: /Reconnect PR/ }).click();
  await expect(
    ui.getByRole("heading", { name: "Reconnect PR", exact: true }),
  ).toBeVisible();
  await expect(ui.getByText("PR context", { exact: true })).toBeVisible();
  await expect(ui.getByLabel("Session status")).toContainText("Not started");
  expect(
    calls.some((c) => ["session_chat", "session_control"].includes(c.name)),
  ).toBe(false);
});
test("chat starts one canonical session and preserves draft on failure", async ({
  page,
}) => {
  let rejected = false;
  const { ui, calls, state } = await host(page, {
    reject: (call) => {
      if (call.name === "session_chat" && !rejected) {
        rejected = true;
        return "The requested action was rejected";
      }
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui.getByLabel("Message", { exact: true }).fill("Investigate reconnect");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText("action was rejected");
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
    "Investigate reconnect",
  );
  expect(state.sessions).toHaveLength(0);
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByLabel("Session status")).toContainText("running");
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue("");
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(2);
  expect(state.sessions).toHaveLength(1);
});
test("uncertain mutation response is not retried", async ({ page }) => {
  const { ui, calls } = await host(page, { lost: "session_chat" });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui.getByLabel("Message", { exact: true }).fill("Inspect once");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText("interrupted");
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
    "Inspect once",
  );
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
});
test("execution reported status and pending events stay separate", async ({
  page,
}) => {
  const state = initial();
  state.sessions = [
    {
      id: "o/r#1",
      itemId: "o/r#1",
      kind: "item",
      execution: "idle",
      reported: "done",
      summary: "Completed requested work",
      pendingCount: 2,
      hold: false,
      agent: { profile: "native" },
      due: new Date().toISOString(),
    },
  ];
  const { ui } = await host(page, { state });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  const status = ui.getByLabel("Session status");
  await expect(status).toContainText("Execution: idle");
  await expect(status).toContainText("Reported status: done");
  await expect(status).toContainText("Pending events: 2");
});
test("native options remain intact and answers address the exact request", async ({
  page,
}) => {
  const state = initial();
  state.questions = [
    {
      id: "q1",
      sessionId: "o/r#1",
      status: "pending",
      blocking: true,
      method: "item/tool/requestUserInput",
      params: {
        questions: [
          {
            id: "scope",
            question: "Which scope?",
            options: [
              { label: "Focused", description: "One fix" },
              { label: "Broad", description: "All fixes" },
            ],
          },
        ],
      },
    },
  ];
  const { ui, calls } = await host(page, { state });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Send answer" }).click();
  await expect(ui.getByText("No pending questions.")).toBeVisible();
  expect(
    calls.find((c) => c.name === "answer_session_question")?.arguments,
  ).toEqual({
    requestId: expect.any(String),
    instanceId: "fixture",
    id: "q1",
    answer: { answers: { scope: { answers: ["Focused"] } } },
  });
});
test("blocked status asks a human and suspends ordinary chat", async ({
  page,
}) => {
  const state = initial();
  state.questions = [
    {
      id: "q1",
      sessionId: "o/r#1",
      status: "pending",
      blocking: true,
      method: "code_factory_report_status",
      params: { summary: "Need a decision", questions: ["Approve scope?"] },
    },
  ];
  const { ui } = await host(page, { state });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui.getByLabel("Message", { exact: true }).fill("Cannot bypass");
  await expect(
    ui.getByRole("button", { name: "Send", exact: true }),
  ).toBeDisabled();
  await ui.getByLabel("Answer: Approve scope?").fill("Use the focused scope");
  await ui.getByRole("button", { name: "Send answer" }).click();
  await expect(
    ui.getByRole("button", { name: "Send", exact: true }),
  ).toBeEnabled();
});
test("native approvals require an explicit decision", async ({ page }) => {
  const state = initial();
  state.questions = [
    {
      id: "approval",
      sessionId: "o/r#1",
      status: "pending",
      blocking: true,
      method: "item/commandExecution/requestApproval",
      params: { reason: "Run requested test" },
    },
  ];
  const { ui, calls } = await host(page, { state });
  await ui.getByRole("button", { name: /Messages/ }).click();
  expect(calls.some((c) => c.name === "answer_session_question")).toBe(false);
  await ui.getByRole("button", { name: "decline", exact: true }).click();
  await expect
    .poll(
      () =>
        calls.find((c) => c.name === "answer_session_question")?.arguments
          .answer,
    )
    .toEqual({ decision: "decline" });
});
test("configuration proposals require explicit confirmation", async ({
  page,
}) => {
  const state = initial();
  state.proposals = [
    { id: "p1", state: "proposed", diff: "quietSeconds: 120 → 30" },
  ];
  const { ui, calls } = await host(page, { state });
  await ui.getByRole("button", { name: "Configuration", exact: true }).click();
  await expect(ui.getByText("quietSeconds: 120 → 30")).toBeVisible();
  expect(calls.some((c) => c.name === "confirm_configuration")).toBe(false);
  await ui.getByRole("button", { name: "Confirm these changes" }).click();
  await expect(ui.getByText("Proposal p1 · applied")).toBeVisible();
  expect(
    calls.find((c) => c.name === "confirm_configuration")?.arguments,
  ).toEqual({
    requestId: expect.any(String),
    instanceId: "fixture",
    id: "p1",
    confirm: true,
  });
});
test("configuration chat and readable effective settings replace forms", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await ui.getByRole("button", { name: "Configuration", exact: true }).click();
  await ui
    .getByLabel("Message", { exact: true })
    .fill("Show my installed profiles");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect
    .poll(() => calls.some((c) => c.name === "configuration_chat"))
    .toBe(true);
  await ui.getByText("Effective settings", { exact: true }).click();
  await expect(ui.locator("pre")).toContainText("quietSeconds");
  await expect(ui.getByRole("button", { name: "Save settings" })).toHaveCount(
    0,
  );
});
test("priorities relationships and PR queues remain available", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await expect(ui.getByText("Reconnect PR · open")).toBeVisible();
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui
    .getByRole("button", { name: "Acknowledge new", exact: true })
    .click();
  await expect
    .poll(() =>
      calls.some(
        (c) => c.name === "user_action" && c.arguments.action === "acknowledge",
      ),
    )
    .toBe(true);
  await ui.getByRole("button", { name: "Mark unread", exact: true }).click();
  await expect
    .poll(() =>
      calls.some(
        (c) => c.name === "user_action" && c.arguments.action === "unread",
      ),
    )
    .toBe(true);
  await ui.getByRole("button", { name: "Record action", exact: true }).click();
  await expect
    .poll(() =>
      calls.some(
        (c) => c.name === "user_action" && c.arguments.action === "acted",
      ),
    )
    .toBe(true);
  await ui.getByLabel("Priority", { exact: true }).selectOption("0");
  await expect
    .poll(
      () =>
        calls.find(
          (c) => c.name === "user_action" && c.arguments.action === "priority",
        )?.arguments.priority,
    )
    .toBe(0);
  await expect(ui.getByLabel("Priority", { exact: true })).toHaveValue("0");
  await ui.getByLabel("Priority", { exact: true }).selectOption("");
  await expect
    .poll(
      () =>
        calls
          .filter(
            (c) =>
              c.name === "user_action" && c.arguments.action === "priority",
          )
          .at(-1)?.arguments.priority,
    )
    .toBeNull();
  await ui.getByText("Relationships", { exact: true }).click();
  await ui.getByLabel("Linked item").fill("o/r#3");
  await ui.getByRole("button", { name: "Link", exact: true }).click();
  await expect
    .poll(() => calls.some((c) => c.name === "link_items"))
    .toBe(true);
  const relation = ui.locator("p").filter({ hasText: "o/r#1 → o/r#3" });
  await expect(relation).toBeVisible();
  await relation
    .getByRole("button", { name: "Remove relationship", exact: true })
    .click();
  await expect(relation).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "link_items").at(-1)?.arguments,
  ).toEqual({
    requestId: expect.any(String),
    instanceId: "fixture",
    source: "o/r#1",
    target: "o/r#3",
    remove: true,
  });
  await ui.getByRole("button", { name: "My PRs", exact: true }).click();
  await expect(ui.getByRole("button", { name: /Reconnect PR/ })).toBeVisible();
});
test("event and rule inspection uses bounded pages and previews without starting work", async ({
  page,
}) => {
  const raw = "full-archive-body-must-stay-off-the-bridge".padEnd(
    64 * 1024,
    "x",
  );
  const archive = {
    events: Array.from({ length: 1000 }, (_, index) => ({
      id: `event-${index + 1}`,
      type: "pull_request",
      action: "synchronize",
      raw,
      preview: {
        text: "A compact event preview",
        originalBytes: raw.length,
        truncated: true,
      },
    })),
    deliveries: [{ id: "delivery-1", state: "accepted" }],
    batches: [{ id: "batch-1", state: "delivered" }],
    rules: [
      {
        id: "rule-1",
        enabled: true,
        skills: ["any-installed-skill"],
        skillsCount: 1,
      },
    ],
  };
  const { ui, calls } = await host(page, { archive });
  await ui.getByRole("button", { name: "Events", exact: true }).click();
  const records = ui.getByRole("list", { name: "Archive records" });
  await expect(records.getByRole("listitem")).toHaveCount(50);
  await expect(ui.getByLabel("Event routing")).toContainText(
    "1–50 of 1000 events",
  );
  await records
    .getByRole("button", {
      name: "event-1 pull_request · synchronize",
      exact: true,
    })
    .click();
  await expect(ui.getByLabel("Selected archive record")).toContainText(
    "A compact event preview",
  );
  await expect(ui.getByLabel("Selected archive record")).toContainText(
    "Preview truncated from 65536 bytes",
  );
  await expect(ui.getByLabel("Event routing")).not.toContainText(
    raw.slice(0, 40),
  );
  await ui.getByRole("button", { name: "Next archive page" }).click();
  await expect(records.getByRole("button").first()).toContainText("event-51");
  await expect(ui.getByLabel("Selected archive record")).toHaveCount(0);
  await ui.getByRole("button", { name: "Previous archive page" }).click();
  await expect(records.getByRole("button").first()).toContainText("event-1");
  await ui.getByLabel("Archive page size").selectOption("200");
  await expect(records.getByRole("listitem")).toHaveCount(200);
  for (const [kind, id] of [
    ["deliveries", "delivery-1"],
    ["batches", "batch-1"],
    ["rules", "rule-1"],
  ]) {
    await ui.getByLabel("Archive kind").selectOption(kind);
    await expect(records.getByRole("button")).toContainText([id]);
  }
  await records.getByRole("button").click();
  await expect(ui.getByLabel("Selected archive record")).toContainText(
    "any-installed-skill",
  );
  expect(
    calls
      .filter((call) => call.name === "factory_events")
      .map(({ arguments: { kind, offset, limit } }) => ({
        kind,
        offset,
        limit,
      })),
  ).toEqual([
    { kind: "events", offset: 0, limit: 50 },
    { kind: "events", offset: 50, limit: 50 },
    { kind: "events", offset: 0, limit: 50 },
    { kind: "events", offset: 0, limit: 200 },
    { kind: "deliveries", offset: 0, limit: 200 },
    { kind: "batches", offset: 0, limit: 200 },
    { kind: "rules", offset: 0, limit: 200 },
  ]);
  await ui
    .getByRole("button", { name: "Read full rules in Configuration" })
    .click();
  await expect(
    ui.getByRole("heading", { name: "Configuration chat" }),
  ).toBeVisible();
  expect(
    calls.some((call) =>
      [
        "session_chat",
        "session_control",
        "configuration_chat",
        "propose_configuration",
        "confirm_configuration",
      ].includes(call.name),
    ),
  ).toBe(false);
});
test("archive refresh hides unavailable data on failure and rejects stale queries", async ({
  page,
}) => {
  let release!: () => void;
  let received!: () => void;
  const reached = new Promise<void>((resolve) => {
    received = resolve;
  });
  const delayed = new Promise<void>((resolve) => {
    release = resolve;
  });
  let failRefresh = false;
  let failBatches = false;
  const { ui, calls, toolCompletions } = await host(page, {
    observeToolCompletions: true,
    archive: {
      events: [{ id: "event-current", action: "synchronize" }],
      deliveries: [{ id: "delivery-stale" }],
      batches: [{ id: "batch-current" }],
      rules: [],
    },
    reject: (call) =>
      call.name === "factory_events" &&
      ((call.arguments.kind === "events" && failRefresh) ||
        (call.arguments.kind === "batches" && failBatches))
        ? "Archive read failed"
        : undefined,
    after: async (call) => {
      if (
        call.name === "factory_events" &&
        call.arguments.kind === "deliveries"
      ) {
        received();
        await delayed;
      }
    },
  });
  try {
    await ui.getByRole("button", { name: "Events", exact: true }).click();
    await ui
      .getByRole("button", { name: "event-current synchronize", exact: true })
      .click();
    failRefresh = true;
    await ui.getByRole("button", { name: "Refresh archive" }).click();
    await expect(ui.getByRole("alert")).toContainText("Archive read failed");
    await expect(ui.getByLabel("Selected archive record")).toHaveCount(0);
    failBatches = true;
    await ui.getByLabel("Archive kind").selectOption("batches");
    await expect(ui.getByRole("alert")).toContainText("Archive read failed");
    await expect(ui.getByRole("list", { name: "Archive records" })).toHaveCount(
      0,
    );
    await expect(ui.getByLabel("Selected archive record")).toHaveCount(0);
    failBatches = false;
    await ui.getByLabel("Archive kind").selectOption("rules");
    await expect(ui.getByLabel("Event routing")).toContainText(
      "0–0 of 0 rules",
    );
    await expect(
      ui.getByRole("button", { name: "Next archive page" }),
    ).toBeDisabled();
    await ui.getByLabel("Archive kind").selectOption("deliveries");
    await reached;
    await ui.getByLabel("Archive kind").selectOption("batches");
    await expect(
      ui.getByRole("list", { name: "Archive records" }),
    ).toContainText("batch-current");
    release();
    // Await this exact superseded invocation after its SDK and React continuation;
    // refreshing the accepted query could otherwise repair a stale-response bug.
    await expect
      .poll(
        () =>
          toolCompletions.filter(
            (call) =>
              call.name === "factory_events" &&
              call.arguments.kind === "deliveries",
          ).length,
      )
      .toBe(1);
    await expect(
      ui.getByRole("status").filter({ hasText: "Loading archive page" }),
    ).toHaveCount(0);
    await expect(
      ui.getByRole("list", { name: "Archive records" }),
    ).toContainText("batch-current");
    await expect(ui.getByLabel("Event routing")).not.toContainText(
      "delivery-stale",
    );
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    failRefresh = false;
    await ui.getByRole("button", { name: "Events", exact: true }).click();
    await expect(
      ui.getByRole("list", { name: "Archive records" }),
    ).toContainText("event-current");
    expect(
      calls.filter(
        (call) =>
          call.name === "factory_events" && call.arguments.kind === "events",
      ),
    ).toHaveLength(3);
  } finally {
    release();
  }
});
test("wrapper keyboard navigation and contrast pass fixture accessibility", async ({
  browser,
}) => {
  for (const theme of ["light", "dark"] as const) {
    const context = await browser.newContext({
      viewport: { width: 390, height: 844 },
      colorScheme: theme,
    });
    const page = await context.newPage();
    try {
      const { ui } = await host(page, { theme });
      await expect(ui.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(
        ui.getByText("Reconnect PR · open", { exact: true }),
      ).toBeVisible();
      const scan = async () => {
        const result = await new AxeBuilder({ page })
          .include({ fromFrames: ["iframe", "html"] })
          .withTags(["wcag2a", "wcag2aa"])
          .analyze();
        expect(
          result.violations,
          `${theme} ${await ui.getByRole("heading", { level: 2 }).textContent()}`,
        ).toEqual([]);
      };
      await scan();
      await ui
        .getByRole("button", { name: "Configuration", exact: true })
        .focus();
      await page.keyboard.press("Enter");
      await expect(
        ui.getByRole("heading", { name: "Configuration chat" }),
      ).toBeVisible();
      await scan();
      await ui.getByRole("button", { name: "Issues", exact: true }).click();
      await ui.getByRole("button", { name: /Fix reconnect/ }).click();
      await expect(
        ui.getByRole("heading", { name: "Fix reconnect" }),
      ).toBeVisible();
      await scan();
    } finally {
      await context.close();
    }
  }
});

test("native approval options preserve exact amendment payloads", async ({
  page,
}) => {
  const state = initial();
  const decision = {
    acceptWithExecpolicyAmendment: { execpolicy_amendment: ["git", "status"] },
  };
  state.questions = [
    {
      id: "amendment",
      sessionId: "o/r#1",
      status: "pending",
      blocking: true,
      method: "item/commandExecution/requestApproval",
      params: { availableDecisions: ["decline", decision] },
    },
  ];
  const { ui, calls } = await host(page, { state });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await expect(
    ui.getByRole("button", { name: "acceptForSession", exact: true }),
  ).toHaveCount(0);
  await ui
    .getByRole("button", { name: JSON.stringify(decision), exact: true })
    .click();
  await expect
    .poll(
      () =>
        calls.find((c) => c.name === "answer_session_question")?.arguments
          .answer,
    )
    .toEqual({ decision });
});
test("streamed output and its completed message render once", async ({
  page,
}) => {
  const { ui } = await host(page, {
    transcript: [
      {
        seq: 1,
        data: {
          method: "item/agentMessage/delta",
          params: { turnId: "t", itemId: "a", delta: "Inspecting " },
        },
      },
      {
        seq: 2,
        data: {
          method: "item/agentMessage/delta",
          params: { turnId: "t", itemId: "a", delta: "work" },
        },
      },
      {
        seq: 3,
        data: {
          method: "item/completed",
          params: {
            turnId: "t",
            item: { id: "a", type: "agentMessage", text: "Inspecting work" },
          },
        },
      },
      {
        seq: 4,
        data: {
          method: "item/completed",
          params: {
            turnId: "t",
            item: {
              id: "b",
              type: "agentMessage",
              text: "A distinct final message",
            },
          },
        },
      },
    ],
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByLabel("Conversation transcript").locator("p"),
  ).toHaveText(["Inspecting work", "A distinct final message"]);
});

test("failed second selection never pairs old context with new actions", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls } = await host(page, {
    before: async (call) => {
      if (call.name === "factory_item" && call.arguments.id === "o/r#2")
        await gate;
    },
    reject: (call) =>
      call.name === "factory_item" && call.arguments.id === "o/r#2"
        ? "PR context unavailable"
        : undefined,
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect(
      ui.getByText("Retain work through disconnection", { exact: true }),
    ).toBeVisible();
    await ui.getByLabel("Message", { exact: true }).fill("Issue draft");
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await ui.getByRole("button", { name: /Reconnect PR/ }).click();
    await expect
      .poll(() =>
        calls.some(
          (c) => c.name === "factory_item" && c.arguments.id === "o/r#2",
        ),
      )
      .toBe(true);
    await expect(
      ui.getByRole("heading", { name: "Reconnect PR", exact: true }),
    ).toBeVisible();
    await expect(
      ui.getByText("Retain work through disconnection", { exact: true }),
    ).toHaveCount(0);
    await expect(
      ui.getByRole("button", { name: "Open on GitHub", exact: true }),
    ).toHaveCount(0);
    await ui.getByLabel("Message", { exact: true }).fill("PR draft");
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeDisabled();
    release();
    await expect(ui.getByRole("alert")).toContainText("PR context unavailable");
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "PR draft",
    );
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeDisabled();
    expect(
      calls.filter(
        (c) =>
          c.name === "session_chat" ||
          (c.name === "user_action" && c.arguments.action !== "read"),
      ),
    ).toHaveLength(0);
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Issue draft",
    );
  } finally {
    release();
  }
});

test("Back restores the originating query filter and row focus with isolated drafts", async ({
  page,
}) => {
  const { ui } = await host(page);
  await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
  await ui.getByLabel("Search inbox").fill("PR context");
  await ui.getByLabel("Inbox filter").selectOption("all");
  await ui.getByRole("button", { name: /Reconnect PR/ }).click();
  await expect(ui.getByText("PR context", { exact: true })).toBeVisible();
  await ui.getByLabel("Message", { exact: true }).fill("PR retained draft");
  await ui.getByRole("button", { name: "Back", exact: true }).click();
  await expect(
    ui.getByRole("heading", { name: "Review PRs", exact: true }),
  ).toBeVisible();
  await expect(ui.getByLabel("Search inbox")).toHaveValue("PR context");
  await expect(ui.getByLabel("Inbox filter")).toHaveValue("all");
  await expect(ui.getByRole("button", { name: /Reconnect PR/ })).toBeFocused();
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await expect(ui.getByLabel("Search inbox")).toHaveValue("");
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue("");
  await ui.getByLabel("Message", { exact: true }).fill("Issue retained draft");
  await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
  await ui.getByRole("button", { name: /Reconnect PR/ }).click();
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
    "PR retained draft",
  );
});

test("search filters and bounded pages apply to the complete saved inbox", async ({
  page,
}) => {
  const state = initial(),
    base = state.inbox[0];
  state.inbox = Array.from({ length: 211 }, (_, i) => ({
    ...structuredClone(base),
    item: {
      ...base.item,
      id: `o/r#${i + 10}`,
      number: i + 10,
      title: `Ticket ${String(i).padStart(3, "0")}`,
      body: i === 205 ? "Needle in body" : "Ordinary body",
      state: i % 5 === 0 ? "closed" : "open",
      author: i === 205 ? "special-owner" : "owner",
    },
    unread: i % 2 === 0,
    user: { new: i % 3 === 0 },
    linkedPullRequests: [],
  }));
  const { ui, calls } = await host(page, { state });
  await ui.getByLabel("Inbox filter").selectOption("all");
  await expect(ui.locator(".inbox-row")).toHaveCount(50);
  await ui
    .getByRole("button", { name: "Show more items", exact: true })
    .click();
  await expect(ui.locator(".inbox-row")).toHaveCount(100);
  await ui
    .getByRole("button", { name: "Show more items", exact: true })
    .click();
  await expect(ui.locator(".inbox-row")).toHaveCount(150);
  await ui
    .getByRole("button", { name: "Show more items", exact: true })
    .click();
  await expect(ui.locator(".inbox-row")).toHaveCount(200);
  await ui.getByRole("button", { name: "Next page", exact: true }).click();
  await expect(ui.locator(".inbox-row")).toHaveCount(11);
  await expect(ui.getByLabel("Inbox result count")).toContainText(
    "211 matching",
  );
  await ui.getByRole("button", { name: "Previous page", exact: true }).click();
  await expect(ui.locator(".inbox-row")).toHaveCount(200);
  await ui.getByLabel("Search inbox").fill("nEeDlE iN bOdY");
  await expect(ui.locator(".inbox-row")).toHaveCount(1);
  await expect(ui.getByRole("button", { name: /Ticket 205/ })).toBeVisible();
  const last = calls.filter((c) => c.name === "factory_state").at(-1)!;
  expect(last.arguments).toMatchObject({
    offset: 0,
    limit: 50,
    search: "nEeDlE iN bOdY",
  });
  await ui.getByLabel("Inbox filter").selectOption("new");
  await expect(
    ui.getByText("No matching items.", { exact: true }),
  ).toBeVisible();
  await ui.getByLabel("Search inbox").fill("");
  await expect(ui.getByLabel("Inbox result count")).toContainText(
    "71 matching",
  );
  await ui.getByLabel("Inbox filter").selectOption("unread");
  await expect(ui.getByLabel("Inbox result count")).toContainText(
    "106 matching",
  );
  expect(
    calls
      .filter((c) => c.name === "factory_state")
      .every((c) => c.arguments.limit <= 200),
  ).toBe(true);
});

test("a late query response cannot replace a newer inbox query", async ({
  page,
}) => {
  const state = initial();
  state.inbox[0].item.title = "Fast result";
  let release!: () => void,
    finished = false;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls } = await host(page, {
    state,
    before: async (call) => {
      if (call.name === "factory_state" && call.arguments.search === "slow") {
        await gate;
        finished = true;
      }
    },
  });
  try {
    await ui.getByLabel("Search inbox").fill("slow");
    await expect
      .poll(() =>
        calls.some(
          (c) => c.name === "factory_state" && c.arguments.search === "slow",
        ),
      )
      .toBe(true);
    await ui.getByLabel("Search inbox").fill("Fast");
    await expect(ui.getByRole("button", { name: /Fast result/ })).toBeVisible();
    release();
    await expect.poll(() => finished).toBe(true);
    await page.evaluate(() => new Promise(requestAnimationFrame));
    await expect(ui.getByRole("button", { name: /Fast result/ })).toBeVisible();
    await expect(ui.getByLabel("Search inbox")).toHaveValue("Fast");
  } finally {
    release();
  }
});

test("uncertainty recovery uses durable delivery state rather than error text", async ({
  page,
}) => {
  const state = initial();
  state.sessions = [
    {
      id: "o/r#1",
      itemId: "o/r#1",
      kind: "item",
      execution: "interrupted",
      pendingCount: 0,
      deliveryState: "uncertain",
      hold: true,
      agent: {},
      error: "context deadline exceeded",
    },
  ];
  const { ui, calls } = await host(page, { state });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui
    .getByRole("button", {
      name: "Explicitly retry uncertain delivery",
      exact: true,
    })
    .click();
  await expect
    .poll(
      () => calls.find((c) => c.name === "resolve_session_delivery")?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "o/r#1",
      decision: "retry",
    });
  await expect(
    ui.getByRole("button", {
      name: "Explicitly retry uncertain delivery",
      exact: true,
    }),
  ).toHaveCount(0);
  state.sessions[0].deliveryState = "uncertain";
  state.sessions[0].error = "Codex process disconnected";
  await ui
    .getByRole("button", { name: "Discard uncertain delivery", exact: true })
    .click();
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "resolve_session_delivery").at(-1)
          ?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "o/r#1",
      decision: "discard",
    });
  state.sessions[0].threadPending = true;
  state.sessions[0].hold = true;
  state.sessions[0].error = "thread creation timed out";
  const retry = ui.getByRole("button", {
    name: "Explicitly retry uncertain delivery",
    exact: true,
  });
  await expect(retry).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Discard uncertain delivery", exact: true }),
  ).toHaveCount(0);
  await retry.click();
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "resolve_session_delivery").at(-1)
          ?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "o/r#1",
      decision: "retry",
    });
  expect(
    calls.some((c) => ["session_chat", "session_control"].includes(c.name)),
  ).toBe(false);
});

test("a pending send does not disable another session's native answer", async ({
  page,
}) => {
  const state = initial();
  state.questions = [
    {
      id: "other-question",
      sessionId: "o/r#2",
      method: "item/tool/requestUserInput",
      status: "pending",
      blocking: true,
      params: { questions: [{ id: "scope", question: "Choose B scope" }] },
    },
  ];
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls } = await host(page, {
    state,
    before: async (call) => {
      if (call.name === "session_chat") await gate;
    },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await ui
      .getByLabel("Message", { exact: true })
      .fill("Slow first attachment");
    await ui.getByRole("button", { name: "Send", exact: true }).click();
    await expect
      .poll(() => calls.some((c) => c.name === "session_chat"))
      .toBe(true);
    await ui.getByRole("button", { name: /Messages/ }).click();
    await ui.getByLabel("Answer: Choose B scope").fill("Focused B");
    await expect(
      ui.getByRole("button", { name: "Send answer", exact: true }),
    ).toBeEnabled();
    await ui.getByRole("button", { name: "Send answer", exact: true }).click();
    await expect
      .poll(
        () =>
          calls.find((c) => c.name === "answer_session_question")?.arguments,
      )
      .toEqual({
        requestId: expect.any(String),
        instanceId: "fixture",
        id: "other-question",
        answer: { answers: { scope: { answers: ["Focused B"] } } },
      });
    await expect(
      ui.getByText("No pending questions.", { exact: true }),
    ).toBeVisible();
  } finally {
    release();
  }
});

test("elicitation preserves schema responses rejected drafts and explicit decisions", async ({
  page,
}) => {
  const state = initial();
  const makeQuestion = (id: string, mode = "form") => ({
    id,
    sessionId: "o/r#1",
    method: "mcpServer/elicitation/request",
    status: "pending",
    blocking: true,
    params: {
      mode,
      message: `Enter name for ${id}`,
      requestedSchema: {
        type: "object",
        required: ["name"],
        properties: { name: { type: "string" } },
      },
    },
  });
  state.questions = [makeQuestion("form")];
  const { ui, calls } = await host(page, {
    state,
    reject: (call) =>
      call.name === "answer_session_question" &&
      call.arguments.answer.action === "accept" &&
      typeof call.arguments.answer.content?.name !== "string"
        ? "name must be a string"
        : undefined,
  });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await expect(
    ui.getByText("Enter name for form", { exact: true }),
  ).toBeVisible();
  await expect(ui.locator("pre")).toContainText('"required"');
  await ui.getByLabel("Response JSON").fill("{invalid");
  await ui.getByRole("button", { name: "Send answer", exact: true }).click();
  await expect(ui.getByRole("alert")).toBeVisible();
  expect(
    calls.filter((c) => c.name === "answer_session_question"),
  ).toHaveLength(0);
  const rejected = '{"action":"accept","content":{"name":42}}';
  await ui.getByLabel("Response JSON").fill(rejected);
  await ui.getByRole("button", { name: "Send answer", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText("name must be a string");
  await expect(ui.getByLabel("Response JSON")).toHaveValue(rejected);
  await ui
    .getByLabel("Response JSON")
    .fill('{"action":"accept","content":{"name":"Ada"}}');
  await ui.getByRole("button", { name: "Send answer", exact: true }).click();
  await expect(
    ui.getByText("No pending questions.", { exact: true }),
  ).toBeVisible();
  expect(
    calls.filter((c) => c.name === "answer_session_question").at(-1)?.arguments,
  ).toEqual({
    requestId: expect.any(String),
    instanceId: "fixture",
    id: "form",
    answer: { action: "accept", content: { name: "Ada" } },
  });
  state.questions.push(makeQuestion("cancel"));
  await ui.getByRole("button", { name: "Cancel request", exact: true }).click();
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "answer_session_question").at(-1)
          ?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "cancel",
      answer: { action: "cancel" },
    });
  state.questions.push(makeQuestion("decline"));
  await ui
    .getByRole("button", { name: "Decline request", exact: true })
    .click();
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "answer_session_question").at(-1)
          ?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "decline",
      answer: { action: "decline" },
    });
});

test("URL elicitation opens through the host and native verification is never fabricated", async ({
  page,
}) => {
  const state = initial();
  state.questions = [
    {
      id: "url",
      sessionId: "o/r#1",
      method: "mcpServer/elicitation/request",
      status: "pending",
      blocking: true,
      params: {
        mode: "url",
        message: "Authorize the connection",
        url: "https://example.com/authorization",
        elicitationId: "url-1",
      },
    },
  ];
  const { ui, calls, openedLinks } = await host(page, { state });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await ui
    .getByRole("button", { name: "Open authorization", exact: true })
    .click();
  await expect
    .poll(() => openedLinks)
    .toEqual(["https://example.com/authorization"]);
  expect(
    calls.filter((c) => c.name === "answer_session_question"),
  ).toHaveLength(0);
  await ui
    .getByRole("button", { name: "Continue after authorization", exact: true })
    .click();
  await expect
    .poll(
      () => calls.find((c) => c.name === "answer_session_question")?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "url",
      answer: { action: "accept" },
    });
  state.questions.push({
    id: "device",
    sessionId: "o/r#1",
    method: "mcpServer/elicitation/request",
    status: "pending",
    blocking: true,
    params: {
      mode: "openai/userVerification",
      challenge: "native-challenge",
      description: "Device verification required",
    },
  });
  await expect(ui.getByText(/requires native host support/)).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Send answer", exact: true }),
  ).toHaveCount(0);
  await ui.getByRole("button", { name: "Cancel request", exact: true }).click();
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "answer_session_question").at(-1)
          ?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "device",
      answer: { action: "cancel" },
    });
});

test("native secret inputs retain rejected answers without rendering secret text", async ({
  page,
}) => {
  const state = initial();
  state.questions = [
    {
      id: "secret",
      sessionId: "o/r#1",
      method: "item/tool/requestUserInput",
      status: "pending",
      blocking: true,
      params: {
        questions: [
          {
            id: "token",
            question: "Provide private fixture value",
            isSecret: true,
          },
        ],
      },
    },
  ];
  let rejected = false;
  const { ui, calls } = await host(page, {
    state,
    reject: (call) => {
      if (call.name === "answer_session_question" && !rejected) {
        rejected = true;
        return "Answer refused by fixture";
      }
    },
  });
  await ui.getByRole("button", { name: /Messages/ }).click();
  const input = ui.getByLabel("Answer: Provide private fixture value");
  await expect(input).toHaveAttribute("type", "password");
  await input.fill("PRIVATE_FIXTURE_VALUE");
  await ui.getByRole("button", { name: "Send answer", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText("Answer refused");
  await expect(input).toHaveValue("PRIVATE_FIXTURE_VALUE");
  await expect(
    ui.getByText("PRIVATE_FIXTURE_VALUE", { exact: true }),
  ).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "answer_session_question"),
  ).toHaveLength(1);
  await ui.getByRole("button", { name: "Send answer", exact: true }).click();
  await expect(
    ui.getByText("No pending questions.", { exact: true }),
  ).toBeVisible();
  expect(
    calls.filter((c) => c.name === "answer_session_question").at(-1)?.arguments,
  ).toEqual({
    requestId: expect.any(String),
    instanceId: "fixture",
    id: "secret",
    answer: { answers: { token: { answers: ["PRIVATE_FIXTURE_VALUE"] } } },
  });
});

test("session controls preserve the selected target and require explicit clicks", async ({
  page,
}) => {
  const state = initial();
  state.sessions = [
    {
      id: "o/r#1",
      itemId: "o/r#1",
      kind: "item",
      execution: "idle",
      pendingCount: 0,
      hold: false,
      agent: {},
    },
  ];
  const { ui, calls } = await host(page, { state });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  expect(calls.some((c) => c.name === "session_control")).toBe(false);
  for (const [label, action] of [
    ["Interrupt", "interrupt"],
    ["Resume", "resume"],
    ["Clean worktree", "cleanup"],
  ]) {
    await ui.getByRole("button", { name: label, exact: true }).click();
    await expect
      .poll(
        () =>
          calls.filter((c) => c.name === "session_control").at(-1)?.arguments,
      )
      .toEqual({
        requestId: expect.any(String),
        instanceId: "fixture",
        id: "o/r#1",
        action,
      });
  }
  await expect(ui.getByLabel("Session status")).toContainText("cleaned");
});

test("same-item source and relationship versions refresh and Back reopens saved detail", async ({
  page,
}) => {
  const { ui, state, calls, relationships } = await host(page);
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByText("Retain work through disconnection", { exact: true }),
  ).toBeVisible();
  state.inbox[0].item.revision = "v2";
  state.inbox[0].item.title = "Updated reconnect";
  state.inbox[0].item.body = "New saved source";
  await expect(
    ui.getByRole("heading", { name: "Updated reconnect", exact: true }),
  ).toBeVisible();
  await expect(ui.getByText("New saved source", { exact: true })).toBeVisible();
  relationships.push({
    source: "o/r#1",
    target: "o/r#3",
    evidence: "fixture",
    manual: true,
  });
  await ui.getByText("Relationships", { exact: true }).click();
  await expect(
    ui.locator("p").filter({ hasText: "o/r#1 → o/r#3" }),
  ).toBeVisible();
  const before = calls.filter((c) => c.name === "factory_item").length;
  await ui.getByRole("button", { name: "Back", exact: true }).click();
  await ui.getByRole("button", { name: /Updated reconnect/ }).click();
  await expect(ui.getByText("New saved source", { exact: true })).toBeVisible();
  expect(calls.filter((c) => c.name === "factory_item").length).toBeGreaterThan(
    before,
  );
});

test("reopening a failed same-item selection retries detail without launching work", async ({
  page,
}) => {
  let fail = true;
  const { ui, calls } = await host(page, {
    reject: (call) =>
      call.name === "factory_item" && fail
        ? "Temporary detail failure"
        : undefined,
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(ui.getByRole("alert")).toContainText("Temporary detail failure");
  expect(
    calls.some(
      (c) => c.name === "user_action" && c.arguments.action === "read",
    ),
  ).toBe(false);
  await ui.getByRole("button", { name: "Back", exact: true }).click();
  fail = false;
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByText("Retain work through disconnection", { exact: true }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "factory_item")).toHaveLength(2);
  expect(
    calls.some((c) => ["session_chat", "session_control"].includes(c.name)),
  ).toBe(false);
});

test("abandoned delayed detail stays unread and accepted detail acknowledges its displayed revision", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls, state, toolCompletions } = await host(page, {
    observeToolCompletions: true,
    before: async (call) => {
      if (call.name === "factory_item" && call.arguments.id === "o/r#1")
        await gate;
    },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect
      .poll(() => calls.some((c) => c.name === "factory_item"))
      .toBe(true);
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await ui.getByRole("button", { name: /Reconnect PR/ }).click();
    await expect(ui.getByText("PR context", { exact: true })).toBeVisible();
    expect(
      toolCompletions.some(
        (call) => call.name === "factory_item" && call.arguments.id === "o/r#1",
      ),
    ).toBe(false);
    release();
    // This exact invocation must finish actual bridge decoding and its two-frame
    // React continuation barrier; the already completed PR request cannot satisfy it.
    await expect
      .poll(
        () =>
          toolCompletions.filter(
            (call) =>
              call.name === "factory_item" && call.arguments.id === "o/r#1",
          ).length,
      )
      .toBe(1);
    await expect
      .poll(() =>
        calls
          .filter(
            (c) => c.name === "user_action" && c.arguments.action === "read",
          )
          .map((c) => c.arguments),
      )
      .toEqual([
        {
          requestId: expect.any(String),
          instanceId: "fixture",
          id: "o/r#2",
          action: "read",
          revision: "v1",
        },
      ]);
    expect(state.inbox[0].unread).toBe(true);
    expect(
      calls
        .filter(
          (c) => c.name === "user_action" && c.arguments.action === "read",
        )
        .map((c) => c.arguments.id),
    ).toEqual(["o/r#2"]);
    await expect(
      ui.getByText("Retain work through disconnection", { exact: true }),
    ).toHaveCount(0);
    await expect(
      ui.getByRole("heading", { name: "Reconnect PR", exact: true }),
    ).toBeVisible();
    // A later intentional selection accepts current detail and acknowledges the
    // displayed revision once, proving the abandoned response did not poison reuse.
    state.inbox[0].item.revision = "v2";
    state.inbox[0].item.body = "Accepted after abandoned response";
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect(
      ui.getByText("Accepted after abandoned response", { exact: true }),
    ).toBeVisible();
    await expect
      .poll(
        () =>
          toolCompletions.filter(
            (call) =>
              call.name === "factory_item" && call.arguments.id === "o/r#1",
          ).length,
      )
      .toBe(2);
    expect(
      calls
        .filter(
          (call) =>
            call.name === "user_action" && call.arguments.action === "read",
        )
        .map((call) => call.arguments),
    ).toEqual([
      {
        requestId: expect.any(String),
        instanceId: "fixture",
        id: "o/r#2",
        action: "read",
        revision: "v1",
      },
      {
        requestId: expect.any(String),
        instanceId: "fixture",
        id: "o/r#1",
        action: "read",
        revision: "v2",
      },
    ]);
    expect(state.inbox[0].unread).toBe(false);
  } finally {
    release();
  }
});

test("a superseded detail version is never displayed or acknowledged", async ({
  page,
}) => {
  const state = initial();
  let changed = false;
  const { ui, calls } = await host(page, {
    state,
    before: async (call) => {
      if (call.name === "factory_item" && !changed) {
        changed = true;
        state.inbox[0].item.revision = "v2";
        state.inbox[0].item.body = "Latest accepted detail";
      }
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByText("Latest accepted detail", { exact: true }),
  ).toBeVisible();
  await expect
    .poll(() =>
      calls
        .filter(
          (c) => c.name === "user_action" && c.arguments.action === "read",
        )
        .map((c) => c.arguments),
    )
    .toEqual([
      {
        requestId: expect.any(String),
        instanceId: "fixture",
        id: "o/r#1",
        action: "read",
        revision: "v2",
      },
    ]);
  expect(calls.filter((c) => c.name === "factory_item")).toHaveLength(2);
});

test("retained transcript drains available pages without polling delays", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    transcriptPage: (after) => {
      if (after >= 1200) return { events: [], cursor: after, hasMore: false };
      const cursor = after + 100;
      return {
        events: Array.from({ length: 100 }, (_, i) => ({
          seq: after + i + 1,
          data: {
            method: "item/completed",
            params: {
              turnId: "history",
              item: {
                id: `message-${after + i}`,
                type: "agentMessage",
                text: `History row ${after + i}`,
              },
            },
          },
        })),
        cursor,
        hasMore: cursor < 1200,
      };
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByLabel("Conversation transcript").locator("p").last(),
  ).toHaveText("History row 1199", { timeout: 5000 });
  await expect(
    ui.getByLabel("Conversation transcript").locator("p"),
  ).toHaveCount(1000);
  expect(
    calls
      .filter((c) => c.name === "session_transcript")
      .slice(0, 12)
      .map((c) => c.arguments.after),
  ).toEqual(Array.from({ length: 12 }, (_, i) => i * 100));
});

test("boot filter controls hydrate using only supported inbox filters", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/src/wrapper.tsx*", async (route) => {
    await gate;
    await route.continue();
  });
  const starting = host(page);
  const ui = page.frameLocator("iframe");
  try {
    await expect(ui.locator("#boot-controls select option")).toHaveText([
      "open",
      "all",
      "new",
      "unread",
    ]);
    await ui.getByLabel("Inbox filter").selectOption("unread");
    release();
    const { calls } = await starting;
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("unread");
    expect(
      calls.find((c) => c.name === "factory_state")?.arguments.filter,
    ).toBe("unread");
  } finally {
    release();
  }
});

test("fragmented transcript waits for complete ordered sets across pages", async ({
  page,
}) => {
  const text = 'Unicode 💡, quotes " and slash \\ with newline\n'.repeat(1200);
  const raw = JSON.stringify({
    method: "item/completed",
    params: {
      turnId: "large",
      item: { id: "large-a", type: "agentMessage", text },
    },
  });
  const width = Math.ceil(raw.length / 3);
  const fragment = (index: number) => ({
    seq: index + 1,
    data: {
      fragmentId: "large",
      fragmentIndex: index,
      fragmentCount: 3,
      fragment: raw.slice(index * width, (index + 1) * width),
    },
  });
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui } = await host(page, {
    before: async (call) => {
      if (call.name === "session_transcript" && call.arguments.after === 1)
        await gate;
    },
    transcriptPage: (after) =>
      after === 0
        ? { events: [fragment(0)], cursor: 1, hasMore: true }
        : after === 1
          ? {
              events: [
                fragment(1),
                fragment(2),
                {
                  seq: 4,
                  data: {
                    method: "item/completed",
                    params: {
                      turnId: "large",
                      item: {
                        id: "distinct",
                        type: "agentMessage",
                        text: "Distinct complete output",
                      },
                    },
                  },
                },
                {
                  seq: 5,
                  data: {
                    fragmentId: "tail",
                    fragmentIndex: 0,
                    fragmentCount: 2,
                    fragment: '{"text":"INCOMPLETE"',
                  },
                },
              ],
              cursor: 5,
              hasMore: false,
            }
          : { events: [], cursor: 5, hasMore: false },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect(
      ui.getByLabel("Conversation transcript").locator("p"),
    ).toHaveCount(0);
    release();
    await expect(
      ui.getByLabel("Conversation transcript").locator("p"),
    ).toHaveText([text, "Distinct complete output"]);
    await expect(ui.getByText("INCOMPLETE", { exact: true })).toHaveCount(0);
  } finally {
    release();
  }
});

test("idle state polls use change versions and mutations refresh without losing the view", async ({
  page,
}) => {
  const { ui, calls, state, stateReplies } = await host(page);
  await expect(ui.getByRole("button", { name: /Fix reconnect/ })).toBeVisible();
  await expect
    .poll(() => stateReplies.filter((r) => r.unchanged).length)
    .toBeGreaterThanOrEqual(2);
  expect(stateReplies.filter((r) => !r.unchanged)).toHaveLength(1);
  expect(
    calls
      .filter((c) => c.name === "factory_state")
      .slice(1)
      .every((c) => c.arguments.sinceVersion === 1),
  ).toBe(true);
  state.inbox[0].item.title = "Changed saved row";
  state.inbox[0].item.revision = "v2";
  await expect(
    ui.getByRole("button", { name: /Changed saved row/ }),
  ).toBeVisible();
  expect(stateReplies.filter((r) => !r.unchanged)).toHaveLength(2);
  await ui.getByRole("button", { name: "My PRs", exact: true }).click();
  await expect(ui.getByRole("button", { name: /Reconnect PR/ })).toBeVisible();
  expect(
    calls.find((c) => c.name === "factory_state" && c.arguments.view === "mine")
      ?.arguments.sinceVersion,
  ).toBeUndefined();
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Changed saved row/ }),
  ).toBeVisible();
  const returned = calls
    .filter((c) => c.name === "factory_state" && c.arguments.view === "issues")
    .at(-1)!;
  expect(returned.arguments.sinceVersion).toBeUndefined();
  await ui.getByLabel("Search inbox").fill("nothing matches");
  await expect(
    ui.getByText("No matching items.", { exact: true }),
  ).toBeVisible();
  const searched = calls
    .filter(
      (c) =>
        c.name === "factory_state" && c.arguments.search === "nothing matches",
    )
    .at(-1)!;
  expect(searched.arguments.sinceVersion).toBeUndefined();
});

test("a delayed state version cannot replace a newer accepted refresh", async ({
  page,
}) => {
  const state = initial();
  let held = false;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls, nextStateCallAfter } = await host(page, {
    state,
    before: async (call) => {
      if (
        call.name === "factory_state" &&
        call.arguments.sinceVersion === 1 &&
        !held
      ) {
        state.inbox[0].item.title = "Delayed stale row";
        state.inbox[0].item.revision = "v2";
      }
    },
    after: async (call, data) => {
      if (
        call.name === "factory_state" &&
        (data as any)?.version === 2 &&
        !held
      ) {
        held = true;
        await gate;
      }
    },
  });
  try {
    await expect.poll(() => held).toBe(true);
    state.inbox[0].item.title = "Latest saved row";
    state.inbox[0].item.revision = "v3";
    await ui.getByRole("button", { name: "Sync", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Latest saved row/ }),
    ).toBeVisible();
    const beforeRelease = calls.filter(
      (call) => call.name === "factory_state",
    ).length;
    release();
    // The held poll schedules its successor only after consuming this response.
    // Inspect that first successor, even if its own reply immediately refreshes UI.
    const nextPoll = await nextStateCallAfter(beforeRelease);
    expect(nextPoll.arguments.sinceVersion).toBe(3);
    await expect(
      ui.getByRole("button", { name: /Latest saved row/ }),
    ).toBeVisible();
    await expect(
      ui.getByRole("button", { name: /Delayed stale row/ }),
    ).toHaveCount(0);
  } finally {
    release();
  }
});

test("accepted slow mutation polls one identity and recovers a lost polling response", async ({
  page,
}) => {
  let complete = false;
  const { ui, calls, state } = await host(page, {
    deferred: {
      name: "session_chat",
      losePoll: 5,
      complete: () => complete,
    },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await ui.getByLabel("Message", { exact: true }).fill("Inspect once");
    await ui.getByRole("button", { name: "Send", exact: true }).click();
    const pollCount = () =>
      calls.filter((c) => c.name === "operation_status").length;
    // Five consecutive failures exceed the ordinary three-attempt read budget.
    await expect.poll(pollCount).toBeGreaterThan(5);
    // Completion stays explicitly gated even if assertions are scheduled late.
    const beforeAssertions = pollCount();
    await expect.poll(pollCount).toBeGreaterThan(beforeAssertions + 2);
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeDisabled();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Inspect once",
    );
    expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
    complete = true;
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue("");
    await ui.getByLabel("Message", { exact: true }).fill("Next draft");
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeEnabled();
    expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
    expect(state.sessions).toHaveLength(1);
    expect(
      calls
        .filter((c) => c.name === "operation_status")
        .every(
          (c) =>
            c.arguments.requestId ===
            calls.find((c) => c.name === "session_chat")?.arguments.requestId,
        ),
    ).toBe(true);
  } finally {
    complete = true;
  }
});

test("delayed operation error preserves the draft without repeating the action", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    deferred: { name: "session_chat", error: "Saved operation failed" },
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui.getByLabel("Message", { exact: true }).fill("Keep this draft");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText("Saved operation failed");
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
    "Keep this draft",
  );
  await expect(
    ui.getByRole("button", { name: "Send", exact: true }),
  ).toBeEnabled();
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
});

test("accepted operation reports terminal provider HTTP503 without polling again", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    deferred: {
      name: "session_chat",
      error: "GitHub request failed (HTTP 503)",
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui
    .getByLabel("Message", { exact: true })
    .fill("Keep provider failure draft");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "GitHub request failed (HTTP 503)",
  );
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
    "Keep provider failure draft",
  );
  await expect(
    ui.getByRole("button", { name: "Send", exact: true }),
  ).toBeEnabled();
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
  const polls = calls.filter((c) => c.name === "operation_status");
  expect(polls).toHaveLength(3);
  expect(
    polls.every(
      (c) =>
        c.arguments.requestId ===
        calls.find((c) => c.name === "session_chat")?.arguments.requestId,
    ),
  ).toBe(true);
});

test("accepted mutation retains its identity through HTTP503 poll outages", async ({
  page,
}) => {
  let failures = 0;
  let complete = false;
  const { ui, calls } = await host(page, {
    deferred: { name: "session_chat", complete: () => complete },
    before: async (call) => {
      if (call.name === "operation_status" && failures++ < 5)
        throw new Error(
          'Error POSTing to endpoint (HTTP503): {"error":"outcome_unknown"}',
        );
    },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await ui.getByLabel("Message", { exact: true }).fill("Keep exact identity");
    await ui.getByRole("button", { name: "Send", exact: true }).click();
    await expect
      .poll(() => calls.filter((c) => c.name === "operation_status").length)
      .toBeGreaterThan(5);
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeDisabled();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Keep exact identity",
    );
    expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
    complete = true;
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue("");
    expect(
      calls
        .filter((c) => c.name === "operation_status")
        .every(
          (c) =>
            c.arguments.requestId ===
            calls.find((c) => c.name === "session_chat")?.arguments.requestId,
        ),
    ).toBe(true);
  } finally {
    complete = true;
  }
});

test("HTTP503 after mutation delivery preserves the draft without replay", async ({
  page,
}) => {
  const { ui, calls, state } = await host(page, {
    after: async (call) => {
      if (call.name === "session_chat")
        throw new Error(
          'Error POSTing to endpoint (HTTP503): {"error":"outcome_unknown"}',
        );
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui
    .getByLabel("Message", { exact: true })
    .fill("Inspect saved state first");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText("outcome may be unknown");
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
    "Inspect saved state first",
  );
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
  expect(state.sessions).toHaveLength(1);
});

test("oversized accepted result ends polling with draft retained and no replay", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    deferred: { name: "session_chat" },
    before: async (call) => {
      if (call.name === "operation_status")
        throw new Error(
          'Error POSTing to endpoint (HTTP500): {"error":"response_limit_exceeded","message":"Inspect saved state and use a narrower read."}',
        );
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await ui
    .getByLabel("Message", { exact: true })
    .fill("Keep oversized result draft");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText("response_limit_exceeded");
  await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
    "Keep oversized result draft",
  );
  await expect(
    ui.getByRole("button", { name: "Send", exact: true }),
  ).toBeEnabled();
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "operation_status")).toHaveLength(1);
});

test("submitting feedback precedes the bridge and queued delivery preserves drafts across navigation", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let completed = false;
  const { ui, calls } = await host(page, {
    observeToolStarts: true,
    deferred: { name: "session_chat", complete: () => completed },
    before: async (call) => {
      if (call.name === "session_chat") await gate;
    },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await ui
      .getByLabel("Message", { exact: true })
      .fill("Retain until delivered");
    const frame = page.frames().find((frame) => frame.parentFrame());
    await frame!.evaluate(() => {
      const button = [...document.querySelectorAll("button")].find(
        (b) => b.textContent === "Send",
      )!;
      button.addEventListener(
        "click",
        () => {
          const start = performance.now();
          const observer = new MutationObserver(() => {
            if (
              [...document.querySelectorAll('[role="status"]')].some(
                (node) => node.textContent === "Submitting…",
              )
            ) {
              (window as any).feedbackMS = performance.now() - start;
              observer.disconnect();
            }
          });
          observer.observe(document.body, {
            subtree: true,
            childList: true,
            characterData: true,
          });
        },
        { once: true },
      );
    });
    await ui.getByRole("button", { name: "Send", exact: true }).click();
    await expect(ui.getByText("Submitting…", { exact: true })).toBeVisible();
    const submission = await frame!.evaluate(() =>
      (window as any).fixtureToolStarts.find(
        (call: any) => call.name === "session_chat",
      ),
    );
    expect(submission.body).toContain("Submitting…");
    expect(
      await frame!.evaluate(() => (window as any).feedbackMS),
    ).toBeLessThan(100);
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Retain until delivered",
    );
    release();
    await expect(ui.getByText(/Queued…|Running…/)).toBeVisible();
    await expect(
      ui.getByText("Message delivered", { exact: true }),
    ).toHaveCount(0);
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Reconnect PR/ }),
    ).toBeVisible();
    await ui.getByRole("button", { name: /Reconnect PR/ }).click();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue("");
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Retain until delivered",
    );
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeDisabled();
    completed = true;
    await expect(
      ui.getByText("Message delivered", { exact: true }),
    ).toBeVisible();
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue("");
    expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(1);
  } finally {
    release();
    completed = true;
  }
});

test("pending saved revisions hide stale context and disable dependent actions", async ({
  page,
}) => {
  let pending = false;
  let holdDetail = false;
  let releaseDetail!: () => void;
  const detailGate = new Promise<void>((resolve) => {
    releaseDetail = resolve;
  });
  const { ui, state, calls } = await host(page, {
    observeToolStarts: true,
    before: async (call) => {
      if (holdDetail && call.name === "factory_item") await detailGate;
    },
    response: (call) =>
      call.name === "factory_state" && pending
        ? { state: "pending", revision: 99 }
        : undefined,
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByText("Retain work through disconnection", { exact: true }),
  ).toBeVisible();
  pending = true;
  state.inbox[0].item.body = "New current source";
  state.inbox[0].item.revision = "v2";
  await expect(
    ui.getByRole("status").filter({ hasText: "Item context is unavailable" }),
  ).toBeVisible();
  await expect(
    ui.getByText("Retain work through disconnection", { exact: true }),
  ).toHaveCount(0);
  await ui
    .getByLabel("Message", { exact: true })
    .fill("Wait for current source");
  await expect(
    ui.getByRole("button", { name: "Send", exact: true }),
  ).toBeDisabled();
  const before = calls.filter((c) => c.name === "factory_state").length;
  await expect
    .poll(() => calls.filter((c) => c.name === "factory_state").length)
    .toBeGreaterThan(before + 1);
  expect(
    calls.filter(
      (c) => c.name === "user_action" && c.arguments.revision === "v2",
    ),
  ).toHaveLength(0);
  holdDetail = true;
  const detailCalls = calls.filter(
    (call) => call.name === "factory_item",
  ).length;
  pending = false;
  try {
    await expect
      .poll(() => calls.filter((call) => call.name === "factory_item").length)
      .toBeGreaterThan(detailCalls);
    expect(
      calls.filter(
        (call) =>
          call.name === "user_action" && call.arguments.revision === "v2",
      ),
    ).toHaveLength(0);
  } finally {
    releaseDetail();
  }
  await expect(
    ui.getByText("New current source", { exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Send", exact: true }),
  ).toBeEnabled();
  const frame = page.frames().find((frame) => frame.parentFrame())!;
  await expect
    .poll(() =>
      frame.evaluate(() =>
        (window as any).fixtureToolStarts.filter(
          (call: any) =>
            call.name === "user_action" && call.arguments.revision === "v2",
        ),
      ),
    )
    .toHaveLength(1);
  const acknowledgment = await frame.evaluate(() =>
    (window as any).fixtureToolStarts.find(
      (call: any) =>
        call.name === "user_action" && call.arguments.revision === "v2",
    ),
  );
  expect(acknowledgment.arguments.action).toBe("read");
  expect(acknowledgment.body).toContain("New current source");
  expect(acknowledgment.body).not.toContain(
    "Retain work through disconnection",
  );
});

test("question pages retain exact requests beyond the first page", async ({
  page,
}) => {
  const state = initial();
  state.questions = Array.from({ length: 55 }, (_, n) => ({
    id: `q${n}`,
    sessionId: "o/r#1",
    method: "item/tool/requestUserInput",
    status: "pending",
    blocking: true,
    params: {
      questions: [{ id: `choice${n}`, question: `Exact question ${n}` }],
    },
  }));
  const { ui, calls } = await host(page, { state });
  await ui.getByRole("button", { name: /^Messages/ }).click();
  await expect(
    ui.getByRole("form", { name: "Human input request" }),
  ).toHaveCount(50);
  await expect(ui.getByLabel("Question pages")).toContainText(
    "1–50 of 55 questions",
  );
  await ui.getByRole("button", { name: "Next question page" }).click();
  await expect(
    ui.getByRole("form", { name: "Human input request" }),
  ).toHaveCount(5);
  await expect(ui.getByLabel("Question pages")).toContainText(
    "51–55 of 55 questions",
  );
  await ui
    .getByLabel("Answer: Exact question 54", { exact: true })
    .fill("Exact answer on second page");
  await ui
    .getByRole("form", { name: "Human input request" })
    .last()
    .getByRole("button", { name: "Send answer" })
    .click();
  await expect
    .poll(
      () => calls.find((c) => c.name === "answer_session_question")?.arguments,
    )
    .toEqual({
      requestId: expect.any(String),
      instanceId: "fixture",
      id: "q54",
      answer: {
        answers: { choice54: { answers: ["Exact answer on second page"] } },
      },
    });
});

test("an unchanged revision becomes current again after a pending read without repeated acknowledgments", async ({
  page,
}) => {
  let pending = false;
  const { ui, calls, state } = await host(page, {
    response: (call) =>
      call.name === "factory_state" && pending
        ? { state: "pending" }
        : undefined,
  });
  await ui.getByRole("button", { name: /Fix reconnect/ }).click();
  await expect(
    ui.getByText("Retain work through disconnection", { exact: true }),
  ).toBeVisible();
  await expect.poll(() => state.inbox[0].unread).toBe(false);
  await expect
    .poll(
      () =>
        calls.filter(
          (c) => c.name === "factory_state" && c.arguments.sinceVersion >= 2,
        ).length,
    )
    .toBeGreaterThan(0);
  pending = true;
  await expect(
    ui.getByText("Item context is unavailable until loading succeeds.", {
      exact: true,
    }),
  ).toBeVisible();
  pending = false;
  await expect(
    ui.getByText("Retain work through disconnection", { exact: true }),
  ).toBeVisible();
  expect(
    calls.filter(
      (c) => c.name === "user_action" && c.arguments.action === "read",
    ),
  ).toHaveLength(1);
});

test("answering the final question page returns to remaining requests", async ({
  page,
}) => {
  const state = initial();
  state.questions = Array.from({ length: 55 }, (_, n) => ({
    id: `q${n}`,
    sessionId: "o/r#1",
    method: "item/tool/requestUserInput",
    status: "pending",
    blocking: true,
    params: {
      questions: [{ id: `choice${n}`, question: `Exact question ${n}` }],
    },
  }));
  const { ui } = await host(page, { state });
  await ui.getByRole("button", { name: /^Messages/ }).click();
  await ui.getByRole("button", { name: "Next question page" }).click();
  for (let n = 54; n >= 50; n--) {
    await ui
      .getByLabel(`Answer: Exact question ${n}`, { exact: true })
      .fill(`answer ${n}`);
    await ui
      .getByRole("form", { name: "Human input request" })
      .last()
      .getByRole("button", { name: "Send answer" })
      .click();
    await expect(
      ui.getByLabel(`Answer: Exact question ${n}`, { exact: true }),
    ).toHaveCount(0);
  }
  await expect(
    ui.getByRole("form", { name: "Human input request" }),
  ).toHaveCount(50);
  await expect(
    ui.getByLabel("Answer: Exact question 0", { exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByText("No pending questions.", { exact: true }),
  ).toHaveCount(0);
});

test("question text and JSON drafts survive pending reads and navigation", async ({
  page,
}) => {
  let pending = false;
  const state = initial();
  state.questions = [
    {
      id: "free",
      sessionId: "o/r#1",
      method: "item/tool/requestUserInput",
      status: "pending",
      blocking: true,
      params: {
        questions: [{ id: "answer", question: "Retain private answer" }],
      },
    },
    {
      id: "json",
      sessionId: "o/r#1",
      method: "mcpServer/elicitation/request",
      status: "pending",
      blocking: true,
      params: { mode: "form", message: "Retain JSON" },
    },
  ];
  const { ui } = await host(page, {
    state,
    response: (call) =>
      call.name === "factory_state" && pending
        ? { state: "pending" }
        : undefined,
  });
  await ui.getByRole("button", { name: /^Messages/ }).click();
  await ui
    .getByLabel("Answer: Retain private answer", { exact: true })
    .fill("private retained text");
  await ui
    .getByRole("textbox", { name: /^Response JSON/ })
    .fill('{"action":"accept","content":{"value":"retained JSON"}}');
  pending = true;
  await expect(
    ui.getByText("Loading questions…", { exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByLabel("Answer: Retain private answer", { exact: true }),
  ).toHaveCount(0);
  pending = false;
  await expect(
    ui.getByLabel("Answer: Retain private answer", { exact: true }),
  ).toHaveValue("private retained text");
  await expect(ui.getByRole("textbox", { name: /^Response JSON/ })).toHaveValue(
    '{"action":"accept","content":{"value":"retained JSON"}}',
  );
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await ui.getByRole("button", { name: /^Messages/ }).click();
  await expect(
    ui.getByLabel("Answer: Retain private answer", { exact: true }),
  ).toHaveValue("private retained text");
  await expect(ui.getByRole("textbox", { name: /^Response JSON/ })).toHaveValue(
    '{"action":"accept","content":{"value":"retained JSON"}}',
  );
});

for (const outcome of ["uncertain", "lost"]) {
  test(`terminal ${outcome} preserves its classification and draft beside raw errors`, async ({
    page,
  }) => {
    const { ui, calls, state } = await host(page, {
      deferred: { name: "session_chat", polls: 1 },
      response: (call) =>
        call.name === "operation_status"
          ? { state: outcome, error: "context deadline exceeded" }
          : undefined,
      observeToolCompletions: true,
    });
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await ui
      .getByLabel("Message", { exact: true })
      .fill("Inspect before another delivery");
    await ui.getByRole("button", { name: "Send", exact: true }).click();
    await expect(ui.getByRole("alert")).toContainText(
      `Operation ${outcome}; inspect saved state before retrying.`,
    );
    await expect(ui.getByRole("alert")).toContainText(
      "context deadline exceeded",
    );
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Inspect before another delivery",
    );
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeEnabled();
    // A subsequent unrelated read provides a bridge completion boundary after
    // terminal handling; navigation cannot replay this completed call.
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Fix reconnect/ }),
    ).toBeVisible();
    expect(calls.filter((call) => call.name === "session_chat")).toHaveLength(
      1,
    );
    expect(state.sessions).toHaveLength(1);
    const submission = calls.find((call) => call.name === "session_chat")!;
    expect(
      calls
        .filter((call) => call.name === "operation_status")
        .every(
          (call) => call.arguments.requestId === submission.arguments.requestId,
        ),
    ).toBe(true);
  });
}

for (const outcome of ["failed", "uncertain", "lost"]) {
  test(`terminal ${outcome} reports unavailable error details without replay`, async ({
    page,
  }) => {
    const { ui, calls } = await host(page, {
      deferred: { name: "session_chat", polls: 1 },
      response: (call) =>
        call.name === "operation_status"
          ? { state: outcome, errorUnavailable: true }
          : undefined,
    });
    await ui.getByRole("button", { name: /Fix reconnect/ }).click();
    await ui
      .getByLabel("Message", { exact: true })
      .fill("Keep this draft for inspection");
    await ui.getByRole("button", { name: "Send", exact: true }).click();
    await expect(ui.getByRole("alert")).toContainText(
      `Operation ${outcome}; inspect saved state before retrying.`,
    );
    await expect(ui.getByRole("alert")).toContainText(
      "Complete error detail is no longer available.",
    );
    await expect(ui.getByLabel("Message", { exact: true })).toHaveValue(
      "Keep this draft for inspection",
    );
    await expect(
      ui.getByRole("button", { name: "Send", exact: true }),
    ).toBeEnabled();
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await expect(
      ui.getByRole("button", { name: /Fix reconnect/ }),
    ).toBeVisible();
    expect(calls.filter((call) => call.name === "session_chat")).toHaveLength(
      1,
    );
  });
}
