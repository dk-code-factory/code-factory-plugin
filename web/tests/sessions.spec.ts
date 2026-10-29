import { test, expect } from "@playwright/test";
import type { Run, JournalEvent } from "../src/types";
import { initial, host, openItem, showItemDetails, showAgents, scanOwnedApp, retained } from "./fixtures";

test("Open agent targets the newest retained run and keeps message drafts across tabs and runs", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [
    retained(snapshot, "old", "idle", "2026-10-20T00:00:00Z"),
    retained(snapshot, "latest"),
    retained(snapshot, "draft", "prepared", "2026-10-22T00:00:00Z"),
    retained(snapshot, "removed", "cleaned", "2026-10-23T00:00:00Z"),
  ];
  const { ui, calls } = await host(page, { snapshot });
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Inactive",
  );
  await openItem(ui, /Fix reconnect regression/);
  await expect(
    ui.getByRole("button", { name: "New run", exact: true }),
  ).toBeVisible();
  await ui.getByLabel("Message agent").fill("Draft for the newest run");
  await ui.getByText("Details", { exact: true }).click();
  await expect(ui.getByText("/local/latest", { exact: true })).toBeVisible();
  await openItem(ui, /Fix reconnect regression/);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Draft for the newest run",
  );
  await showAgents(ui);
  await ui.locator(".work-history > button.item").first().click();
  await expect(ui.getByLabel("Message agent")).toHaveValue("");
  await ui.getByLabel("Message agent").fill("Draft for the old run");
  await showAgents(ui);
  await ui.locator(".work-history > button.item").nth(1).click();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Draft for the newest run",
  );
  expect(
    calls.filter((c) =>
      ["prepare_launch", "start_run", "send_message"].includes(c.name),
    ),
  ).toHaveLength(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`agent conversation shows human messages and keeps technical activity under Details in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "transcript", "waiting")];
    const params = [
      {
        method: "item/completed",
        params: {
          item: {
            id: "user",
            type: "userMessage",
            content: [{ text: "Please investigate the reconnect failure." }],
          },
        },
      },
      {
        method: "item/agentMessage/delta",
        params: { itemId: "progress", delta: "Checking " },
      },
      {
        method: "item/agentMessage/delta",
        params: { itemId: "progress", delta: "the reconnect path." },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "progress",
            type: "agentMessage",
            phase: "commentary",
            text: "Checking the reconnect path.",
          },
        },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "command",
            type: "commandExecution",
            command: "git status",
            aggregatedOutput: "Command output detail",
          },
        },
      },
      {
        method: "turn/plan/updated",
        params: {
          plan: [{ step: "Inspect reconnect", status: "in_progress" }],
        },
      },
      { method: "turn/diff/updated", params: { diff: "Raw change detail" } },
      {
        method: "item/reasoning/summaryTextDelta",
        params: { itemId: "reasoning", delta: "Internal reasoning summary" },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "files",
            type: "fileChange",
            changes: [{ path: "main.go", diff: "Raw file patch" }],
          },
        },
      },
      {
        method: "provider/chunk",
        params: { delta: "Unstructured output detail" },
      },
      {
        method: "item/completed",
        params: {
          item: {
            id: "final",
            type: "agentMessage",
            phase: "final_answer",
            text: "The retry condition is fixed and verified.",
          },
        },
      },
      {
        method: "error",
        params: {
          error: {
            message: "Visible failure",
            code: "providerFailure",
            diagnostic: "Diagnostic error detail",
          },
        },
      },
    ];
    const transcript = params.map((data, i) => ({
      seq: i + 1,
      kind: "run.output",
      entity: "transcript",
      data,
      at: "2026-10-21T00:00:00Z",
    })) as JournalEvent[];
    snapshot.attention = [
      {
        id: "visible-question",
        runId: "transcript",
        method: "item/tool/requestUserInput",
        blocking: true,
        status: "pending",
        params: {
          questions: [
            {
              id: "scope",
              header: "Scope",
              question: "Which scope should I use?",
              options: [{ label: "Focused", description: "One fix" }],
            },
          ],
        },
      },
    ];
    const { ui, state, calls } = await host(page, {
      snapshot,
      transcript,
      theme,
    });
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    const conversation = ui.locator(".transcript");
    for (const message of [
      "Please investigate the reconnect failure.",
      "Checking the reconnect path.",
      "The retry condition is fixed and verified.",
      "Visible failure",
    ])
      await expect(
        conversation.getByText(message, { exact: true }),
      ).toBeVisible();
    await expect(
      conversation.getByText("Checking the reconnect path.", { exact: true }),
    ).toHaveCount(1);
    const details = ui.locator(".agent-details");
    await expect(details).not.toHaveAttribute("open", "");
    for (const message of [
      "in_progress: Inspect reconnect",
      "Raw change detail",
      "Internal reasoning summary",
      "main.go\nRaw file patch",
      "Unstructured output detail",
      "git status\nCommand output detail",
    ]) {
      await expect(
        conversation.getByText(message, { exact: true }),
      ).toHaveCount(0);
      await expect(ui.getByText(message, { exact: true })).toBeHidden();
    }
    await expect(
      ui.getByRole("button", { name: /Waiting for your answer/ }),
    ).toBeVisible();
    await page.screenshot({
      path: test.info().outputPath(`concise-conversation-${theme}.png`),
    });
    await scanOwnedApp(page, `concise-conversation-${theme}`);
    await details.locator("summary").focus();
    await page.keyboard.press("Enter");
    await expect(
      ui.getByText("git status\nCommand output detail", { exact: true }),
    ).toBeVisible();
    await expect(
      ui.getByText("Internal reasoning summary", { exact: true }),
    ).toBeVisible();
    await expect(
      ui.getByText("in_progress: Inspect reconnect", { exact: true }),
    ).toBeVisible();
    await expect(
      ui.getByText(/"diagnostic": "Diagnostic error detail"/),
    ).toBeVisible();
    await details.locator("summary").click();
    transcript.push({
      seq: 13,
      kind: "run.output",
      entity: "transcript",
      data: {
        method: "item/agentMessage/delta",
        params: {
          itemId: "next-progress",
          delta: "Verification is still running.",
        },
      },
      at: "2026-10-21T00:00:01Z",
    });
    transcript.push({
      seq: 14,
      kind: "run.output",
      entity: "transcript",
      data: {
        method: "commandExecution/outputDelta",
        params: { itemId: "next-command", delta: "New command noise" },
      },
      at: "2026-10-21T00:00:01Z",
    });
    state.cursor++;
    await expect(
      conversation.getByText("Verification is still running.", { exact: true }),
    ).toBeVisible();
    await expect(details).not.toHaveAttribute("open", "");
    await expect(
      ui.getByText("New command noise", { exact: true }),
    ).toBeHidden();
    await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
    await expect(ui.getByRole("dialog")).toContainText(
      "Which scope should I use?",
    );
    await page.keyboard.press("Escape");
    expect(
      calls.filter((c) =>
        ["start_run", "send_message", "answer_request"].includes(c.name),
      ),
    ).toHaveLength(0);
  });
}

test("technical-only activity leaves a concise waiting state in the conversation", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "technical-only", "running")];
  const { ui } = await host(page, {
    snapshot,
    transcript: [
      {
        seq: 1,
        kind: "run.output",
        entity: "technical-only",
        data: {
          method: "item/completed",
          params: {
            item: {
              id: "cmd",
              type: "commandExecution",
              command: "go test ./...",
              aggregatedOutput: "Raw test output",
            },
          },
        },
        at: "2026-10-21T00:00:00Z",
      },
    ],
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.locator(".transcript")).toHaveText(
    "Waiting for an agent update…",
  );
  await expect(
    ui.getByText("go test ./...\nRaw test output", { exact: true }),
  ).toBeHidden();
});

test("transcript recovery keeps output and draft while removing only its own read error", async ({
  page,
}) => {
  let transcriptUnavailable = false;
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "recover-output")];
  const { ui } = await host(page, {
    snapshot,
    transportFault: (name) =>
      transcriptUnavailable && name === "read_transcript",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  await ui.getByLabel("Message agent").fill("Keep the follow-up draft.");
  transcriptUnavailable = true;
  snapshot.cursor++;
  await expect(
    ui.getByRole("status").filter({ hasText: "Retrying conversation" }),
  ).toBeVisible({ timeout: 7000 });
  await expect(
    ui.getByText("Inspecting reconnect logic", { exact: true }),
  ).toBeVisible();
  transcriptUnavailable = false;
  await expect(
    ui.getByRole("status").filter({ hasText: "Retrying conversation" }),
  ).toHaveCount(0, { timeout: 7000 });
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep the follow-up draft.",
  );
  await expect(ui.getByRole("alert")).toHaveCount(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`terminated work retains history and cleanup without allowing continuation in ${theme}`, async ({ page }) => {
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "closed-source", "terminated")];
    snapshot.runs[0].error = "PR is merged; work terminated. History and workspace are retained.";
    const { ui, calls } = await host(page, { snapshot, theme });
    await expect(ui.getByRole("status", { name: "Work status" })).toHaveText("Stopped");
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await expect(ui.getByRole("alert")).toContainText("PR is merged; work terminated");
    await ui.getByLabel("Message agent").fill("Continue anyway");
    await expect(ui.getByRole("button", { name: "Send message", exact: true })).toBeDisabled();
    await ui.getByText("Details", { exact: true }).click();
    await expect(ui.getByRole("button", { name: "Clean up worktree", exact: true })).toBeVisible();
    await expect(ui.getByRole("button", { name: "Interrupt", exact: true })).toHaveCount(0);
    expect(calls.filter(c => ["start_run", "send_message"].includes(c.name))).toHaveLength(0);
  });
  test(`inactive work status and sending a message restore the retained session in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const snapshot = initial();
    snapshot.runs = [retained(snapshot, "wake-session", "interrupted")];
    // A disconnected turn identity must not be sent as an active steering target.
    snapshot.runs[0].turnId = "old-turn";
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { ui, calls } = await host(page, {
      snapshot,
      theme,
      beforeMessage: () => gate,
    });
    await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
      "Inactive",
    );
    await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
    await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
      "Inactive",
    );
    await expect(ui.getByRole("status", { name: "Run status" })).toHaveCount(0);
    await expect(ui.getByRole("button", { name: /Resume agent/ })).toHaveCount(
      0,
    );
    await ui
      .getByLabel("Message agent")
      .fill("Continue this issue with my clarification.");
    const send = ui.getByRole("button", { name: "Send message", exact: true });
    try {
      await send.dblclick();
      await expect(send).toBeDisabled();
      await expect
        .poll(() => calls.filter((c) => c.name === "send_message").length)
        .toBe(1);
      await expect(ui.getByLabel("Message agent")).toHaveValue(
        "Continue this issue with my clarification.",
      );
      release();
      await expect(ui.getByLabel("Message agent")).toHaveValue("");
      await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
        "Active",
      );
      await page.screenshot({
        path: test.info().outputPath(`message-wake-${theme}.png`),
      });
      expect(
        calls.filter((c) => c.name === "send_message")[0].arguments,
      ).toEqual({
        id: "wake-session",
        text: "Continue this issue with my clarification.",
        expectedTurn: "",
      });
      expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
      expect(
        await ui
          .locator("body")
          .evaluate((el) => el.scrollWidth <= el.clientWidth),
      ).toBe(true);
    } finally {
      release();
    }
  });
}

test("saved message clears its draft before agent delivery and shows uncertainty without replay", async ({ page }) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "queued-session", "interrupted")];
  const transcript: JournalEvent[] = [];
  const { ui, calls } = await host(page, { snapshot, transcript, queueMessages: true });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui.getByLabel("Message agent").fill("Save my follow-up before contacting the agent.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByLabel("Message agent")).toHaveValue("");
  await expect(ui.locator(".transcript")).toContainText("Message saved. Awaiting agent delivery.");
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText("Inactive");
  transcript.push({ seq: snapshot.cursor + 1, kind: "run.output", entity: "queued-session", at: new Date().toISOString(), data: { method: "codefactory/messageDelivery", params: { itemId: "saved-message", state: "uncertain", error: "Agent delivery could not be confirmed. Inspect this session before sending again." } } });
  await expect(ui.locator(".transcript")).toContainText("Agent delivery could not be confirmed.");
  await expect(ui.locator(".transcript")).not.toContainText("Awaiting agent delivery");
  await expect(ui.locator(".transcript article")).toHaveCount(1);
  expect(calls.filter(c => c.name === "send_message")).toHaveLength(1);
  expect(calls.filter(c => c.name === "start_run")).toHaveLength(0);
});

test("failed inactive message keeps its draft and never calls Resume or replays the send", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "failed-session", "failed")];
  const { ui, calls } = await host(page, {
    snapshot,
    toolError: {
      name: "send_message",
      message: "Retained worktree is unavailable",
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui.getByLabel("Message agent").fill("Keep this clarification.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Retained worktree is unavailable",
  );
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this clarification.",
  );
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this clarification.",
  );
  expect(calls.filter((c) => c.name === "send_message")).toHaveLength(1);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

test("cleaned work keeps its Finished item status while offering an explicit fresh conversation", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "cleaned-history", "cleaned")];
  const { ui, calls } = await host(page, { snapshot });
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Finished",
  );
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Finished",
  );
  await expect(
    ui.getByRole("region", { name: "New agent conversation" }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  expect(
    calls.filter((c) =>
      ["start_run", "send_message", "prepare_launch"].includes(c.name),
    ),
  ).toHaveLength(0);
});

test("Markdown issue headings do not replace the useful conversation summary", async ({ page }) => {
  const snapshot = initial();
  const paragraph = "The PR capacity helper must clamp exhausted capacity to zero.";
  snapshot.inbox[0].item.body = `## Issue\n\n### Context\n\n${paragraph}\n\n## Scope\n\nNo persistence changes.`;
  const { ui, calls } = await host(page, { snapshot });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const summary = ui.getByRole("region", { name: "Item summary", exact: true });
  await expect(summary).toHaveText(paragraph);
  await showItemDetails(ui);
  await ui.getByText("Full description", { exact: true }).click();
  await expect(ui.getByText("Full description", { exact: true }).locator("..")).toContainText("## Issue");
  await expect(ui.getByText("Full description", { exact: true }).locator("..")).toContainText("No persistence changes.");
  expect(calls.some((c) => ["prepare_launch", "start_run"].includes(c.name))).toBe(false);
});

test("chunked owner and agent completions remain normal conversation across journal pages", async ({ page }) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "chunked-conversation")];
  const transcript: JournalEvent[] = [];
  const event = (method: string, params: Record<string, unknown>) => transcript.push({
    seq: transcript.length + 1, kind: "run.output", entity: "chunked-conversation",
    at: "2026-10-24T01:00:00Z", data: { method, params },
  });
  for (let n = 0; n < 99; n++) event("ignored", {});
  const chunk = (params: Record<string, unknown>) => {
    const bytes = Buffer.from(JSON.stringify(params));
    for (let start = 0; start < bytes.length;) {
      let end = Math.min(start + (32 << 10), bytes.length);
      while (end < bytes.length && (bytes[end] & 0xc0) === 0x80) end--;
      event("item/completed/chunk", { delta: bytes.subarray(start, end).toString() });
      start = end;
    }
  };
  const owner = `Owner message ${'é {quoted "value"} \\ '.repeat(2200)}owner end`;
  const agent = `Agent reply ${'🧭 [nested] "reply" \\ '.repeat(2200)}agent end`;
  chunk({ item: { id: "owner-large", type: "userMessage", content: [{ text: owner }] } });
  event("item/agentMessage/delta", { itemId: "agent-large", delta: "Temporary streamed prefix" });
  chunk({ item: { id: "agent-large", type: "agentMessage", text: agent } });
  chunk({ item: { id: "owner-next", type: "userMessage", content: [{ text: "Adjacent completed owner message" }] } });
  event("item/completed/chunk", { delta: '{"item":{"type":"agentMessage","text":"partial secret' });
  event("item/completed", { item: { id: "command", type: "commandExecution", command: "fixture command", aggregatedOutput: "Technical output only" } });
  event("item/completed/chunk", { delta: "malformed provider JSON" });
  const { ui, calls } = await host(page, { snapshot, transcript });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const conversation = ui.locator('[aria-label="Conversation"]');
  await expect(conversation.getByText(owner, { exact: true })).toBeVisible();
  await expect(conversation.getByText(agent, { exact: true })).toBeVisible();
  await expect(conversation.getByText("Adjacent completed owner message", { exact: true })).toBeVisible();
  await expect(conversation.locator("article").filter({ has: ui.getByText(owner, { exact: true }) }).locator("small")).toHaveText("You");
  await expect(conversation.locator("article").filter({ has: ui.getByText(agent, { exact: true }) }).locator("small")).toHaveText("Agent");
  await expect(conversation.getByText("Temporary streamed prefix", { exact: true })).toHaveCount(0);
  await expect(conversation).not.toContainText("partial secret");
  await expect(conversation).not.toContainText("Technical output only");
  await expect(conversation).not.toContainText("malformed provider JSON");
  const details = ui.locator("details").filter({ has: ui.locator("summary").getByText("Details", { exact: true }) });
  await expect(details).not.toHaveAttribute("open");
  await details.locator("summary").click();
  await expect(ui.getByText(/partial secret/)).toBeVisible();
  await expect(ui.getByText("malformed provider JSON", { exact: true })).toBeVisible();
  expect(calls.filter((c) => c.name === "read_transcript").some((c) => c.arguments.after === 100)).toBe(true);
});

test("full resync resets a retained transcript cursor and reloads saved detail while keeping its message draft",async({page})=>{
  const snapshot=initial();snapshot.cursor=100;
  snapshot.runs=[retained(snapshot,"restored-run")];
  const transcript:JournalEvent[]=[{seq:100,kind:"run.output",entity:"restored-run",at:new Date().toISOString(),data:{method:"item/agentMessage/delta",params:{delta:"Before restore transcript"}}}];
  let restored=false;
  const {ui,state,calls}=await host(page,{snapshot,transcript,readItem:async detail=>({...detail,refreshError:restored?"Restored saved observation":""})});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.locator(".transcript")).toContainText("Before restore transcript");
  await ui.getByLabel("Message agent").fill("Keep my draft across restored history.");
  await expect.poll(()=>calls.filter(c=>c.name==="read_transcript").some(c=>c.arguments.after===100)).toBe(true);
  restored=true;
  transcript.splice(0,transcript.length,{seq:2,kind:"run.output",entity:"restored-run",at:new Date().toISOString(),data:{method:"item/agentMessage/delta",params:{delta:"Restored transcript content"}}});
  state.cursor=3;
  await expect(ui.locator(".transcript")).toContainText("Restored transcript content");
  await expect(ui.locator(".transcript")).not.toContainText("Before restore transcript");
  await expect(ui.getByRole("alert")).toContainText("Restored saved observation");
  await expect(ui.getByLabel("Message agent")).toHaveValue("Keep my draft across restored history.");
  expect(calls.filter(c=>c.name==="read_transcript"&&c.arguments.after===0).length).toBeGreaterThanOrEqual(2);
  expect(calls.filter(c=>["send_message","start_run"].includes(c.name))).toHaveLength(0);
});

test("older retained history stays inspectable across updates and advances only on request",async({page})=>{
  const snapshot=initial();snapshot.cursor=2000;
  snapshot.runs=[retained(snapshot,"large-history")];
  const transcript:JournalEvent[]=Array.from({length:1300},(_,n)=>({seq:n+1,kind:"run.output",entity:"large-history",at:"2026-10-27T12:00:00Z",data:{method:"item/completed",params:{item:{id:"history-"+(n+1),type:"agentMessage",text:"Historical event "+(n+1)}}}}));
  const {ui,state,calls}=await host(page,{snapshot,transcript});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  const history=ui.locator(".transcript");
  await expect(history.getByText("Historical event 1300",{exact:true})).toBeVisible();
  await ui.getByLabel("Message agent").fill("Keep this draft while inspecting older history.");
  await ui.getByRole("button",{name:"View history from the beginning"}).click();
  await expect(ui.getByRole("button",{name:"Load next history page"})).toBeVisible();
  await expect(history.getByText("Historical event 1",{exact:true})).toBeVisible();
  const transcriptReads=calls.filter(c=>c.name==="read_transcript").length;
  await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
    Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>true});
    frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
    Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>false});
    frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
  });
  await history.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
  expect(calls.filter(c=>c.name==="read_transcript")).toHaveLength(transcriptReads);
  await expect(history.getByText("Historical event 1",{exact:true})).toBeVisible();
  const stateReads=calls.filter(c=>c.name==="read_ui_state").length;
  state.cursor++;
  await expect.poll(()=>calls.filter(c=>c.name==="read_ui_state").length).toBeGreaterThan(stateReads);
  await history.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
  expect(calls.filter(c=>c.name==="read_transcript")).toHaveLength(transcriptReads);
  await expect(history.getByText("Historical event 1",{exact:true})).toBeVisible();
  await ui.getByRole("button",{name:"Load next history page"}).click();
  await expect(history.getByText("Historical event 200",{exact:true})).toBeVisible();
  await expect(history.getByText("Historical event 1",{exact:true})).toBeVisible();
  await ui.getByRole("button",{name:"Follow latest history"}).click();
  await expect(history.getByText("Historical event 1300",{exact:true})).toBeVisible();
  await expect(history.getByText("Historical event 1",{exact:true})).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toHaveValue("Keep this draft while inspecting older history.");
  expect(calls.filter(c=>["send_message","start_run"].includes(c.name))).toHaveLength(0);
});

test("transcript-only publications update output without broad state reads",async({page})=>{
  const snapshot=initial();snapshot.runs=[retained(snapshot,"output-only")];
  const transcript:JournalEvent[]=[];
  const {ui,state,calls}=await host(page,{snapshot,transcript,watchDomains:["transcript"]});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  const reads=calls.filter(c=>c.name==="read_ui_state").length;
  transcript.push({seq:10,kind:"run.output",entity:"output-only",at:new Date().toISOString(),data:{method:"item/agentMessage/delta",params:{delta:"Transcript-only update"}}});
  state.cursor++;
  await expect(ui.locator(".transcript")).toContainText("Transcript-only update");
  expect(calls.filter(c=>c.name==="read_ui_state")).toHaveLength(reads);
});

test("continuing transcript publications coalesce while delayed reads still render valid prefixes",async({page})=>{
  const snapshot=initial();snapshot.cursor=100;
  snapshot.runs=[retained(snapshot,"continuous-output")];
  const transcript:JournalEvent[]=[{seq:1,kind:"run.output",entity:"continuous-output",at:new Date().toISOString(),data:{method:"item/agentMessage/delta",params:{delta:"Seed transcript "}}}];
  let active=0,maximum=0;
  const {ui,state}=await host(page,{snapshot,transcript,watchDomains:["transcript"],readTranscript:async result=>{
    active++;maximum=Math.max(maximum,active);
    try{await new Promise(resolve=>setTimeout(resolve,180));return result}finally{active--}
  }});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect(ui.locator(".transcript")).toContainText("Seed transcript");
  let emitted=0;
  const stream=setInterval(()=>{
    emitted++;
    transcript.push({seq:transcript.length+1,kind:"run.output",entity:"continuous-output",at:new Date().toISOString(),data:{method:"item/agentMessage/delta",params:{delta:"Live prefix "+emitted+" "}}});
    state.cursor++;
  },35);
  try{
    await expect(ui.locator(".transcript")).toContainText("Live prefix",{timeout:4000});
    // The interval remains live at this assertion; valid output cannot wait for EOF.
    expect(emitted).toBeGreaterThan(0);
    expect(maximum).toBe(1);
  }finally{clearInterval(stream)}
});

test("hidden retained history pauses pagination and resumes on visibility without a new publication",async({page})=>{
  const snapshot=initial();snapshot.cursor=2000;snapshot.runs=[retained(snapshot,"hidden-history")];
  const transcript:JournalEvent[]=Array.from({length:400},(_,n)=>({seq:n+1,kind:"run.output",entity:"hidden-history",at:new Date().toISOString(),data:{method:"item/completed",params:{item:{id:"hidden-"+(n+1),type:"agentMessage",text:"Retained hidden event "+(n+1)}}}}));
  const {ui,calls}=await host(page,{snapshot,transcript,watchDomains:["transcript"],readTranscript:async result=>{await new Promise(resolve=>setTimeout(resolve,180));return result}});
  await ui.getByRole("button",{name:/Fix reconnect regression/}).click();
  await expect.poll(()=>calls.filter(c=>c.name==="read_transcript").length).toBeGreaterThan(0);
  await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
    Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>true});
    frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
  });
  const reads=calls.filter(c=>c.name==="read_transcript").length;
  // A read already sent at hide may finish, but must not start its next page.
  await expect(ui.locator(".transcript")).toContainText("Retained hidden event");
  await ui.locator("body").evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
  expect(calls.filter(c=>c.name==="read_transcript")).toHaveLength(reads);
  await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
    Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>false});
    frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
  });
  await expect(ui.locator(".transcript")).toContainText("Retained hidden event 400");
});

test("retained review progress exposes incomplete providers and reconciles a verified receipt without reposting", async ({page})=>{
 const snapshot=initial();Object.assign(snapshot.inbox[0].item,{kind:"pr",head:"a".repeat(40),author:"other",reviewRequests:["alex"]});
 const run=retained(snapshot,"retained-review","failed");run.threadId="";run.reviewWorkflow="code-factory-pr-review-v1";run.reviewStage="ordered-reviewers";run.reviewArtifact={path:"/private/result.json",sha256:"b".repeat(64)};run.reviewComplete=false;
 run.reviewResult={complete:false,outcome:"provider_failed",executed_reviewers:["one"],reused_reviewers:[],skipped_reviewers:[{reviewer:"limited",reason:"usage limit"}],failed_reviewers:[{reviewer:"two",reason:"provider unavailable"}],finding_count:0};snapshot.runs=[run];
 const {ui,calls,state,openedLinks}=await host(page,{snapshot,transcript:[]});await ui.getByRole("button",{name:"Review PRs",exact:true}).click();await openItem(ui,/Fix reconnect regression/);
 const workflow=ui.getByRole("region",{name:"Retained review workflow"});await expect(workflow).toContainText("Incomplete review");await expect(workflow).toContainText("Skipped limited: usage limit");await expect(workflow).toContainText("Incomplete two: provider unavailable");
 await expect(workflow.getByRole("button",{name:"Post retained review",exact:true})).toHaveCount(0);
 await ui.getByText("Details",{exact:true}).click();
 await expect(ui.getByRole("button",{name:"Resume review workflow",exact:true})).toBeVisible();
 run.status="idle";state.cursor++;
 await expect(ui.getByRole("button",{name:"Resume review workflow",exact:true})).toHaveCount(0);
 run.status="interrupted";state.cursor++;
 await expect(ui.getByRole("button",{name:"Resume review workflow",exact:true})).toBeVisible();
 run.status="idle";run.reviewComplete=true;run.postingState="uncertain";run.postingOperation="op-1";run.reviewOmissions=[{finding:1,reason:"No diff anchor"}];run.reviewStage="evaluate-posting-policy";state.cursor++;
 await expect(ui.getByRole("button",{name:"Resume review workflow",exact:true})).toHaveCount(0);
 await expect(workflow).toContainText("Finding 2 omitted: No diff anchor");await expect(workflow.getByRole("button",{name:"Post retained review",exact:true})).toBeDisabled();
 await workflow.getByRole("button",{name:"Reconcile review publication",exact:true}).click();
 await expect(workflow).toContainText("Posting: verified");await expect(workflow).toContainText("1 inline comments");await workflow.getByRole("link",{name:"Verified review receipt"}).click();
 await expect.poll(()=>openedLinks).toContain("https://github.com/owner/repo/pull/1#receipt");expect(calls.filter(c=>c.name==="reconcile_review_publication")).toHaveLength(1);expect(calls.filter(c=>c.name==="post_retained_review")).toHaveLength(0);
});

test("retained review explicit posting sends its artifact and displays the server receipt",async({page})=>{
 const snapshot=initial();Object.assign(snapshot.inbox[0].item,{kind:"pr",head:"a".repeat(40),author:"other",reviewRequests:["alex"]});
 const run=retained(snapshot,"post-artifact","idle");run.threadId="";run.reviewWorkflow="code-factory-pr-review-v1";run.reviewStage="finished";run.reviewComplete=true;run.reviewArtifact={path:"/private/result.json",sha256:"b".repeat(64)};snapshot.runs=[run];
 const {ui,calls}=await host(page,{snapshot,transcript:[]});await ui.getByRole("button",{name:"Review PRs",exact:true}).click();await openItem(ui,/Fix reconnect regression/);
 const workflow=ui.getByRole("region",{name:"Retained review workflow"});
 await expect(workflow.getByText("No actionable findings.", {exact:true})).toHaveCount(0);
 run.reviewResult={complete:true,outcome:"completed",executed_reviewers:["one"],reused_reviewers:[],skipped_reviewers:[]};run.reviewStage="evaluate-posting-policy";snapshot.cursor++;
 await expect(workflow).toContainText("Stage: evaluate-posting-policy");
 await expect(workflow.getByText("No actionable findings.", {exact:true})).toHaveCount(0);
 run.reviewResult.finding_count=1;run.reviewStage="finished";snapshot.cursor++;
 await expect(workflow).toContainText("Stage: finished");
 await expect(workflow.getByText("No actionable findings.", {exact:true})).toHaveCount(0);
 run.reviewResult.finding_count=0;snapshot.cursor++;
 await expect(workflow.getByText("No actionable findings.", {exact:true})).toBeVisible();
 await workflow.getByRole("button",{name:"Post retained review",exact:true}).click();
 await expect(workflow.getByRole("link",{name:"Verified review receipt"})).toBeVisible();
 expect(calls.filter(c=>c.name==="post_retained_review")).toHaveLength(1);expect(calls.find(c=>c.name==="post_retained_review")?.arguments).toEqual({runId:run.id,artifact:run.reviewArtifact});
});
