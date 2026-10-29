import { test, expect } from "@playwright/test";
import { initial, host, showSettings, openItem, showItemDetails, showAgents, triage, scanOwnedApp, retained } from "./fixtures";

test("triage does not launch, launch edits and attention answer survive reopening", async ({
  page,
}) => {
  const { ui, calls } = await host(page);
  await openItem(ui, /Fix reconnect regression/);
  await expect(
    ui.getByRole("heading", { name: "Fix reconnect regression", level: 1 }),
  ).toBeVisible();
  await expect
    .poll(() =>
      calls.some(
        (c) => c.name === "item_action" && c.arguments.revision === "rev-1",
      ),
    )
    .toBe(true);
  expect(calls.some((c) => c.name === "start_run")).toBe(false);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  await ui.getByRole("button", { name: "Edit", exact: true }).click();
  await ui
    .getByLabel("Task prompt")
    .fill("Investigate the reconnect race with a focused test.");
  await ui.getByText("Advanced", { exact: true }).click();
  await ui.getByLabel("Model", { exact: true }).fill("local-model");
  await ui.getByRole("button", { name: "Review launch" }).click();
  await expect(
    ui.getByRole("heading", { name: "Review agent launch" }),
  ).toBeVisible();
  expect(calls.some((c) => c.name === "start_run")).toBe(false);
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "prepare_launch").at(-1)?.arguments
          .prompt,
    )
    .toBe("Investigate the reconnect race with a focused test.");
  expect(
    calls.filter((c) => c.name === "prepare_launch").at(-1)?.arguments.model,
  ).toBe("local-model");
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await expect(
    ui.getByRole("dialog", { name: "Agent needs attention" }),
  ).toBeVisible();
  const question = ui.getByRole("group", { name: "Which fix scope?" });
  await expect(question.getByRole("radio")).toHaveCount(2);
  await expect(
    question.getByRole("radio", { name: "Focused One reconnection fix" }),
  ).toBeVisible();
  await expect(
    question.getByRole("radio", { name: "Broad All connection issues" }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "answer_request")).toHaveLength(0);

  // Replace the host document and iframe while the original request is pending.
  // The host's Node-side snapshot survives; no second launch seeds the new view.
  const snapshotsBeforeReopen = calls.filter(
    (c) => c.name === "read_ui_state",
  ).length;
  await page.reload();
  await expect(ui.getByLabel("Search inbox")).toBeVisible();
  await expect.poll(()=>calls.filter((c) => c.name === "read_ui_state").length).toBeGreaterThan(snapshotsBeforeReopen);
  await expect(
    ui.getByRole("button", { name: /^Messages\s*1$/ }),
  ).toBeVisible();
  await showAgents(ui);
  await expect(ui.getByRole("button", { name: /Investigation ·/ })).toHaveCount(
    1,
  );
  await ui.getByRole("button", { name: /Investigation ·/ }).click();
  await ui.getByText("Details", { exact: true }).click();
  await expect(
    ui.getByText("/local/worktrees/run-2", { exact: true }),
  ).toBeVisible();
  await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await expect(question.getByRole("radio")).toHaveCount(2);
  await expect(
    question.getByRole("radio", { name: "Focused One reconnection fix" }),
  ).toBeVisible();
  await expect(
    question.getByRole("radio", { name: "Broad All connection issues" }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(2);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "answer_request")).toHaveLength(0);
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(
    ui.getByRole("dialog", { name: "Agent needs attention" }),
  ).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "answer_request").map((c) => c.arguments),
  ).toEqual([
    {
      id: "attention-1",
      answer: { answers: { scope: { answers: ["Focused"] } } },
    },
  ]);
  await ui.getByRole("button", { name: "Issues", exact: true }).click();
  await showAgents(ui);
  await ui.getByRole("button", { name: /Investigation ·/ }).click();
  await ui.getByLabel("Message agent").fill("Explain the test result.");
  await ui.getByRole("button", { name: "Send message" }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "send_message").length)
    .toBe(1);
  expect(calls.find((c) => c.name === "send_message")?.arguments).toEqual({
    id: "run-2",
    text: "Explain the test result.",
    expectedTurn: "",
  });
  expect(calls.filter((c) => c.name === "start_run").length).toBe(1);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(2);
});

test("GitHub-deferred review shows its retry reason and deadline and can cancel before a thread starts", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].item.kind = "pr";
  const run = retained(snapshot, "github-deferred", "deferred");
  run.threadId = "";
  run.error = "GitHub request budget exhausted; waiting for capacity.";
  run.launchRetryAt = "2026-10-25T18:45:00Z";
  run.resumePending = true;
  snapshot.runs = [run];
  const { ui, calls } = await host(page, { snapshot, transcript: [] });
  await ui.getByRole("button", { name: /^Messages/ }).click();
  await expect(
    ui.getByRole("region", { name: "Waiting for you" }).getByRole("button"),
  ).toHaveCount(0);
  await ui.getByRole("button", { name: "Working", exact: true }).click();
  await expect(ui.getByLabel("Work status")).toHaveText("Waiting for GitHub");
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Work status")).toHaveText("Waiting for GitHub");
  const retry = ui.getByRole("region", { name: "GitHub retry" });
  await expect(retry.getByRole("status")).toHaveText("Waiting for GitHub");
  await expect(retry).toContainText(run.error);
  await expect(retry).toContainText("Next retry:");
  await expect(retry.locator("time")).toHaveAttribute(
    "datetime",
    run.launchRetryAt,
  );
  await expect(retry.locator("time")).toHaveText(
    await page.evaluate(
      (at) => new Date(at).toLocaleString(),
      run.launchRetryAt,
    ),
  );
  await expect(ui.getByRole("alert")).toHaveCount(0);
  await expect(
    ui.getByText("No agent messages yet.", { exact: true }),
  ).toBeVisible();
  await expect(ui.getByText("Waiting for you", { exact: true })).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toBeDisabled();
  await expect(
    ui.getByRole("button", { name: "Send message", exact: true }),
  ).toBeDisabled();
  await ui.getByRole("button", { name: "Cancel retry", exact: true }).click();
  await expect
    .poll(() =>
      calls.filter((c) => c.name === "interrupt_run").map((c) => c.arguments),
    )
    .toEqual([{ id: run.id }]);
  await expect(retry).toHaveCount(0);
  await expect(ui.getByLabel("Work status")).toHaveText("Inactive");
  await expect(ui.getByLabel("Message agent")).toBeDisabled();
  await expect(
    ui.getByRole("button", { name: "Send message", exact: true }),
  ).toBeDisabled();
  expect(
    calls.filter((c) => ["start_run", "send_message"].includes(c.name)),
  ).toHaveLength(0);
});

test("default launch opens review directly and Draft launches reopens its immutable snapshot newest first", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.profiles[0].mode = "plan";
  snapshot.runs = [
    retained(snapshot, "older-draft", "prepared", "2026-10-20T00:00:00Z"),
  ];
  const { ui, state, calls } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  await expect(dialog.getByText("plan", { exact: true })).toBeVisible();
  await expect(dialog).toContainText("read-only · on-request");
  await expect(dialog).toContainText("Ask before publishing");
  await expect(dialog.getByLabel("Task prompt")).toHaveCount(0);
  const prepared = structuredClone(state.runs[0]);
  state.profiles[0].name = "Changed profile";
  state.profiles[0].mode = "default";
  state.profiles[0].sandbox = "workspace-write";
  await dialog.getByRole("button", { name: "Close launch" }).click();
  await showAgents(ui);
  await expect(ui.getByText("Draft launches", { exact: true })).toBeVisible();
  const drafts = ui.getByText("Draft launches", { exact: true }).locator("..");
  await expect(drafts).not.toHaveAttribute("open", "");
  await drafts.locator("summary").click();
  await drafts.getByRole("button").first().click();
  await expect(dialog).toContainText(prepared.profile.name);
  await expect(dialog.getByText("plan", { exact: true })).toBeVisible();
  await expect(dialog).toContainText("read-only · on-request");
  await expect(dialog).toContainText(prepared.prompt);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
  await dialog
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect
    .poll(() => calls.find((c) => c.name === "start_run")?.arguments)
    .toEqual({ id: prepared.id });
});

test("unverifiable Start disables retry and Edit until read_run locates accepted work", async ({
  page,
}) => {
  const options = { loseStartResponse: true, failReadRun: true };
  const { ui, calls } = await host(page, options);
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  const dialog = ui.getByRole("dialog");
  await expect(dialog.getByRole("alert")).toContainText(
    "Start status is uncertain",
  );
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeDisabled();
  await expect(
    dialog.getByRole("button", { name: "Edit", exact: true }),
  ).toBeDisabled();
  options.failReadRun = false;
  await dialog
    .getByRole("button", { name: "Check start status", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
});

test("confirmed unaccepted Start allows an explicit retry of the same snapshot", async ({
  page,
}) => {
  const options = { rejectStart: true };
  const { ui, calls } = await host(page, options);
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  const dialog = ui.getByRole("dialog");
  await expect(dialog.getByRole("alert")).toContainText("still prepared");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  options.rejectStart = false;
  await dialog
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "start_run").map((c) => c.arguments),
  ).toEqual([{ id: "run-1" }, { id: "run-1" }]);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
});

test("item opens a prefilled conversation and first-message Start stays explicit", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 820 });
  const { ui, calls, state } = await host(page);
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("region", { name: "Agent", exact: true }),
  ).toBeVisible();
  await expect(ui.getByRole("region", { name: "Item summary" })).toContainText(
    "Connections stop after a brief outage.",
  );
  await expect(
    ui.getByRole("region", { name: "Item", exact: true }),
  ).toHaveCount(0);
  const chat = ui.getByRole("region", { name: "New agent conversation" });
  await expect(chat.getByLabel("Message agent")).toHaveValue(
    "Investigate Fix reconnect regression.\nConnections stop after a brief outage.",
  );
  await expect(ui.locator(".item-details")).not.toHaveAttribute("open", "");
  await expect(
    ui.getByRole("button", { name: "Open agent", exact: true }),
  ).toHaveCount(0);
  await page.screenshot({ path: test.info().outputPath("first-message.png") });
  await scanOwnedApp(page, "first-message");
  await showItemDetails(ui);
  await ui.getByText("Work history", { exact: true }).click();
  await expect(ui.getByText("No work history.", { exact: true })).toBeVisible();
  expect(
    calls.some((call) => ["prepare_launch", "start_run"].includes(call.name)),
  ).toBe(false);
  await chat
    .getByLabel("Message agent")
    .fill("Explain the retry failure and suggest a safe fix.");
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(chat.getByLabel("Message agent")).toHaveValue(
    "Explain the retry failure and suggest a safe fix.",
  );
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(calls.find((c) => c.name === "prepare_launch")!.arguments.prompt).toBe(
    "Explain the retry failure and suggest a safe fix.",
  );
  expect(
    calls.filter((c) => c.name === "start_run").map((c) => c.arguments.id),
  ).toEqual([state.runs[0].id]);
  await expect(ui.getByRole("dialog")).toHaveCount(0);
});

test("first-message launch uncertainty survives leaving and reopening its item", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    rejectStart: true,
    failReadRun: true,
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByRole("region", { name: "New agent conversation" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toContainText(
    "Start status is uncertain",
  );
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeDisabled();
  await ui
    .getByRole("button", { name: "Check start status", exact: true })
    .click();
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
});

test("first-message launch reviews changed authority before starting its prepared snapshot", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const chat = ui.getByRole("region", { name: "New agent conversation" });
  await expect(chat).toContainText("read-only");
  state.profiles[0].sandbox = "workspace-write";
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(chat.getByRole("alert")).toContainText(
    "Launch settings changed",
  );
  await expect(chat).toContainText("workspace-write");
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(
    calls.filter((c) => c.name === "start_run").map((c) => c.arguments.id),
  ).toEqual([state.runs[0].id]);
});

test("first-message lost Start response locates accepted work without another launch", async ({
  page,
}) => {
  const { ui, calls, state } = await host(page, { loseStartResponse: true });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByRole("region", { name: "New agent conversation" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  expect(calls.find((c) => c.name === "read_run")!.arguments.id).toBe(
    state.runs[0].id,
  );
});

test("a late first-message Start cannot replace another item's draft", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second task";
  snapshot.inbox.push(second);
  const { ui, calls } = await host(page, {
    snapshot,
    beforeStartResponse: () => {
      // Keep snapshot reads behind the accepted Start response. Only accepting
      // its waiting run can make the pending question count as a message.
      snapshot.runs[0].status = "prepared";
      return gate;
    },
  });
  try {
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await ui
      .getByRole("region", { name: "New agent conversation" })
      .getByRole("button", { name: "Start agent", exact: true })
      .click();
    await expect
      .poll(() => calls.filter((c) => c.name === "start_run").length)
      .toBe(1);
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await ui.getByRole("button", { name: /Second task/ }).click();
    await ui
      .getByLabel("Message agent")
      .fill("Keep the second task's unsubmitted message.");
    await expect(ui.getByRole("button", { name: "Messages", exact: true })).toBeVisible();
    release();
    // This badge is derived from acceptRun's returned run, not a host-call or
    // response-sent signal. Wait for that React update before checking selection.
    await expect(ui.getByRole("button", { name: "Messages 1", exact: true })).toBeVisible();
    await expect(
      ui.getByRole("heading", { name: "Second task", exact: true }),
    ).toBeVisible();
    await expect(ui.getByLabel("Message agent")).toHaveValue(
      "Keep the second task's unsubmitted message.",
    );
    await expect(
      ui.getByRole("region", { name: "New agent conversation" }),
    ).toBeVisible();
    // A later navigation reads the still-behind prepared projection after the
    // accepted response has already reached React; it must not undo acceptance.
    const reads=calls.filter(c=>c.name==="read_ui_state").length;
    await ui.getByRole("button",{name:"Issues",exact:true}).click();
    await ui.getByRole("button",{name:/Second task/}).click();
    await expect.poll(()=>calls.filter(c=>c.name==="read_ui_state").length).toBeGreaterThan(reads);
    await ui.locator("body").evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
    await expect(ui.getByRole("button",{name:"Messages 1",exact:true})).toBeVisible();
    await expect(ui.getByLabel("Message agent")).toHaveValue("Keep the second task's unsubmitted message.");
    expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(1);
    expect(calls.filter((c) => c.name === "start_run")).toHaveLength(1);
  } finally {
    release();
  }
});

test("first-message preparation errors retain the editable draft without starting", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { failPreparation: true });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const chat = ui.getByRole("region", { name: "New agent conversation" });
  await chat
    .getByLabel("Message agent")
    .fill("Keep this first-message draft after failure.");
  await chat.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect(chat.getByRole("alert")).toContainText(
    "Fixture preparation denied",
  );
  await expect(chat.getByLabel("Message agent")).toHaveValue(
    "Keep this first-message draft after failure.",
  );
  await expect(chat.getByLabel("Message agent")).toBeEditable();
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`own PR review rejection preserves the first message without starting in ${theme}`, async ({ page }) => {
    const snapshot = initial();
    snapshot.inbox[0].item.kind = "pr";
    snapshot.inbox[0].queues = ["mine"];
    snapshot.profiles[0].id = "review";
    snapshot.profiles[0].name = "PR review";
    const { ui, calls } = await host(page, {
      snapshot,
      theme,
      toolError: { name: "prepare_launch", message: "you cannot review your own pull request (owner/repo#1)" },
    });
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    const chat = ui.getByRole("region", { name: "New agent conversation" });
    await chat.getByLabel("Agent pipeline").selectOption("review");
    await chat.getByLabel("Message agent").fill("Review this pull request.");
    await chat.getByRole("button", { name: "Start agent", exact: true }).click();
    await expect(chat.getByRole("alert")).toContainText("you cannot review your own pull request");
    await expect(chat.getByLabel("Message agent")).toHaveValue("Review this pull request.");
    expect(calls.filter(c => c.name === "prepare_launch")).toHaveLength(1);
    expect(calls.filter(c => c.name === "start_run")).toHaveLength(0);
    expect(snapshot.runs).toHaveLength(0);
  });
}

test("transient polling outage recovers without a stale error banner or lost first message", async ({
  page,
}) => {
  let disconnected = false;
  const { ui, calls } = await host(page, {
    transportFault: (name) => disconnected && name === "watch_ui_changes",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByLabel("Message agent")
    .fill("Keep this draft through reconnect.");
  disconnected = true;
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Reconnecting…", { timeout: 7000 });
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this draft through reconnect.",
  );
  disconnected = false;
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Connected", { timeout: 7000 });
  await expect(ui.getByRole("alert")).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this draft through reconnect.",
  );
  expect(
    calls.some((c) =>
      ["prepare_launch", "start_run", "send_message"].includes(c.name),
    ),
  ).toBe(false);
});

test("first-message pipeline selection preserves an edited prompt and launches the selected preset", async ({
  page,
}) => {
  const state = initial();
  state.profiles.push({
    ...state.profiles[0],
    id: "review",
    name: "PR review",
    prompt: "Review {{title}}",
  });
  const { ui, calls } = await host(page, { snapshot: state });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Agent pipeline")).toHaveValue("implement");
  await ui
    .getByLabel("Message agent")
    .fill("Inspect the regression without publishing.");
  await ui.getByLabel("Agent pipeline").selectOption("review");
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Inspect the regression without publishing.",
  );
  expect(calls.filter((c) => c.name === "prepare_launch")).toHaveLength(0);
  await ui.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect
    .poll(() => calls.filter((c) => c.name === "start_run").length)
    .toBe(1);
  expect(
    calls.find((c) => c.name === "prepare_launch")!.arguments.profileId,
  ).toBe("review");
});

test("linked PR changes refresh full selected details without consuming another read or launch",async({page})=>{
 const snapshot=initial();
 snapshot.inbox[0].linkedPullRequests=[{id:"owner/repo#2",title:"Linked implementation",url:"https://github.com/owner/repo/pull/2",state:"open",draft:false,head:"abc",reviewDecision:"approved",ci:"success",syncedAt:new Date().toISOString(),checks:[{name:"Initial CI",status:"completed",conclusion:"success",url:""}]}];
 const {ui,calls,state}=await host(page,{snapshot,readItem:async detail=>({...detail,linkedPullRequests:structuredClone(snapshot.inbox[0].linkedPullRequests)})});
 await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
 const linked=ui.getByRole("region",{name:"Linked pull requests",exact:true});
 await expect(linked).toContainText("success");
 snapshot.inbox[0].linkedPullRequests![0].ci="failure";
 snapshot.inbox[0].linkedPullRequests![0].checks=[{name:"Latest CI",status:"completed",conclusion:"failure",url:""}];
 state.cursor++;
 await expect(linked).toContainText("failure");
 await linked.getByText("CI checks",{exact:true}).click();
 await expect(linked.getByText(/Latest CI/)).toBeVisible();
 expect(calls.filter(c=>c.name==="item_action"&&c.arguments.action==="read")).toHaveLength(1);
 expect(calls.some(c=>["prepare_launch","start_run"].includes(c.name))).toBe(false);
});

test("explicit full-access launch shows never approvals and authorized publishing", async ({ page }) => {
  const snapshot = initial();
  snapshot.profiles[0].sandbox = "danger-full-access";
  snapshot.profiles[0].approval = "never";
  snapshot.profiles[0].publishing = "authorized";
  const { ui, calls, state } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "New run", exact: true }).click();
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  await expect(dialog).toContainText("danger-full-access · never");
  await expect(dialog).toContainText("Authorized");
  expect(calls.filter(c => c.name === "start_run")).toHaveLength(0);
  await dialog.getByRole("button", { name: "Start agent", exact: true }).click();
  await expect.poll(() => calls.filter(c => c.name === "start_run").length).toBe(1);
  expect(state.runs[0].profile).toMatchObject({ sandbox: "danger-full-access", approval: "never", publishing: "authorized" });
  await showSettings(page);
  await expect(ui.getByText(/Retained review publication follows the posting policy/)).toBeVisible();
});

test("launch customization loads saved skills only when its deferred view opens",async({page})=>{
  const snapshot=initial();
  snapshot.skills=[{id:"saved-skill",name:"Saved skill",description:"Saved fixture skill",path:"/fixture/skill",content:"",managed:true}];
  const {ui,calls}=await host(page,{snapshot});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await showItemDetails(ui);
  expect(calls.filter(c=>c.name==="read_ui_state").every(c=>!(c.arguments.views as string[]).includes("launch"))).toBe(true);
  await ui.getByRole("button",{name:"New run",exact:true}).click();
  await ui.getByRole("button",{name:"Edit",exact:true}).click();
  await ui.getByText("Advanced",{exact:true}).click();
  await ui.getByRole("checkbox",{name:"Saved skill",exact:true}).check();
  await ui.getByRole("button",{name:"Review launch",exact:true}).click();
  await expect.poll(()=>calls.filter(c=>c.name==="prepare_launch").some(c=>(c.arguments.skillIds as string[]|undefined)?.includes("saved-skill"))).toBe(true);
  expect(calls.filter(c=>c.name==="read_ui_state").some(c=>(c.arguments.views as string[]).includes("launch"))).toBe(true);
  expect(calls.filter(c=>c.name==="start_run")).toHaveLength(0);
});

test("full resync clears a local Start acceptance overlay without replaying the launch",async({page})=>{
  const snapshot=initial();snapshot.cursor=100;
  const {ui,state,calls}=await host(page,{snapshot,beforeStartResponse:()=>{snapshot.runs[0].status="prepared";return Promise.resolve()}});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await ui.getByRole("region",{name:"New agent conversation"}).getByRole("button",{name:"Start agent",exact:true}).click();
  await expect(ui.getByLabel("Session status")).toContainText("waiting");
  state.cursor=3;
  await expect(ui.getByLabel("Session status")).toContainText("prepared");
  await expect(ui.getByRole("button",{name:"Messages",exact:true})).toBeVisible();
  expect(calls.filter(c=>c.name==="start_run")).toHaveLength(1);
});

test("a delayed Start status check cannot install acceptance after full resync",async({page})=>{
  const snapshot=initial();snapshot.cursor=100;
  let release:()=>void=()=>{};const gate=new Promise<void>(resolve=>{release=resolve});let checking=false;
  const {ui,state,calls}=await host(page,{snapshot,loseStartResponse:true,readRun:async data=>{checking=true;await gate;return data}});
  try{
    await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
    await ui.getByRole("region",{name:"New agent conversation"}).getByRole("button",{name:"Start agent",exact:true}).click();
    await expect.poll(()=>checking).toBe(true);
    state.runs[0].status="prepared";state.cursor=3;
    await expect.poll(()=>calls.some(c=>c.name==="watch_ui_changes"&&c.arguments.after===3)).toBe(true);
    release();
    await expect(ui.getByRole("alert").filter({hasText:"Saved state changed during the request"})).toBeVisible();
    await expect(ui.getByRole("button",{name:"Messages",exact:true})).toBeVisible();
    expect(calls.filter(c=>c.name==="start_run")).toHaveLength(1);
  }finally{release()}
});

test("launch posting inheritance stays unresolved on policy failure and retries without losing explicit overrides", async ({ page }) => {
  const snapshot = initial();
  Object.assign(snapshot.inbox[0].item, { kind: "pr", head: "a".repeat(40), author: "other", reviewRequests: ["alex"] });
  snapshot.config.autoPostReview = true;
  snapshot.repositories = [{ id: snapshot.inbox[0].item.repository, checkout: "", base: "", lastSync: "", error: "", paused: false, autoPostReview: false }];
  let failPolicy = false;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const { ui, calls } = await host(page, { snapshot, beforeReadReviewPolicy: async () => {
    if (failPolicy) { await gate; throw new Error("Policy read unavailable"); }
  } });
  await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByRole("region", { name: "PR review posting policy" })).toContainText("Off · Source: repository");
  failPolicy = true;
  try {
    await ui.getByRole("button", { name: "New run", exact: true }).click();
    const dialog = ui.getByRole("dialog", { name: "Launch agent" });
    await expect(dialog).toContainText("Off · Source: repository");
    await dialog.getByRole("button", { name: "Edit", exact: true }).click();
    await dialog.getByText("Advanced", { exact: true }).click();
    const posting = dialog.getByLabel("Launch automatic review posting");
    await expect(posting.locator("option").first()).toHaveText("Inherit (unresolved)");
    await expect(dialog.getByRole("status")).toHaveText("Loading inherited posting policy…");
    release();
    await expect(dialog.getByRole("alert")).toContainText("Unable to load inherited posting policy");
    await expect(dialog.getByRole("alert")).toContainText("Policy read unavailable");
    await expect(posting.locator("option").first()).toHaveText("Inherit (unresolved)");
    await posting.selectOption("true");
    await dialog.getByRole("button", { name: "Review launch", exact: true }).click();
    await expect(dialog).toContainText("On · Source: launch");
    expect(calls.filter((call) => call.name === "prepare_launch").at(-1)?.arguments.autoPostReview).toBe(true);
    await dialog.getByRole("button", { name: "Edit", exact: true }).click();
    await dialog.getByText("Advanced", { exact: true }).click();
    failPolicy = false;
    await dialog.getByRole("button", { name: "Retry posting policy", exact: true }).click();
    await expect(posting.locator("option").first()).toHaveText("Inherit (Off)");
    await expect(posting).toHaveValue("true");
    await expect(dialog.getByRole("alert")).toHaveCount(0);
    await expect(dialog.getByRole("status")).toHaveCount(0);
    expect(calls.filter((call) => ["start_run", "post_retained_review"].includes(call.name))).toHaveLength(0);
  } finally { release(); }
});

test("PR posting policy and launch override show effective source without starting work", async ({page})=>{
 const snapshot=initial();Object.assign(snapshot.inbox[0].item,{kind:"pr",head:"a".repeat(40),author:"other",reviewRequests:["alex"]});snapshot.config.autoPostReview=true;
 const {ui,calls}=await host(page,{snapshot});await ui.getByRole("button",{name:"Review PRs",exact:true}).click();await openItem(ui,/Fix reconnect regression/);
 const policy=ui.getByRole("region",{name:"PR review posting policy"});await expect(policy).toContainText("On · Source: global");
 await policy.getByLabel("PR automatic review posting").selectOption("false");await expect(policy).toContainText("Off · Source: pr");
 await policy.getByLabel("PR automatic review posting").selectOption("inherit");await expect(policy).toContainText("On · Source: global");
 await ui.getByRole("button",{name:"New run",exact:true}).click();const dialog=ui.getByRole("dialog",{name:"Launch agent"});
 await dialog.getByRole("button",{name:"Edit",exact:true}).click();await dialog.getByText("Advanced",{exact:true}).click();
 await dialog.getByLabel("Launch automatic review posting").selectOption("false");await expect(dialog.getByLabel("Launch automatic review posting").locator("option").first()).toHaveText("Inherit (On)");await dialog.getByRole("button",{name:"Review launch",exact:true}).click();
 await expect(dialog).toContainText("Off · Source: launch");expect(calls.filter(c=>c.name==="prepare_launch").at(-1)?.arguments.autoPostReview).toBe(false);
 expect(calls.filter(c=>["start_run","post_retained_review"].includes(c.name))).toHaveLength(0);
});

test("saved review policy changes refresh an open PR without polling or starting work", async ({page}) => {
 const snapshot=initial();
 Object.assign(snapshot.inbox[0].item,{kind:"pr",head:"a".repeat(40),author:"other",reviewRequests:["alex"]});
 const {ui,state,calls}=await host(page,{snapshot,watchDomains:["config","pr-review-policy"]});
 await ui.getByRole("button",{name:"Review PRs",exact:true}).click();
 await openItem(ui,/Fix reconnect regression/);
 const policy=ui.getByRole("region",{name:"PR review posting policy"});
 await expect(policy).toContainText("Off · Source: global");
 state.config.autoPostReview=true;state.cursor++;
 await expect(policy).toContainText("On · Source: global");
 expect(calls.filter(c=>["start_run","post_retained_review"].includes(c.name))).toHaveLength(0);
});
