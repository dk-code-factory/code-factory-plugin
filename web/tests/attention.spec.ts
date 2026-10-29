import { test, expect } from "@playwright/test";
import type { Snapshot } from "../src/types";
import { initial, host, openItem, openWaiting, retained } from "./fixtures";

test("empty filtered inbox offers Clear filters and hides zero attention", async ({
  page,
}) => {
  const { ui } = await host(page);
  await expect(ui.getByRole("button", { name: /^Messages\s*1$/ })).toHaveCount(
    0,
  );
  await ui.getByLabel("Search inbox").fill("no such item");
  await expect(
    ui.getByText("No matching items", { exact: true }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "Clear filters", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  ).toBeVisible();
  await expect(ui.getByLabel("Search inbox")).toHaveValue("");
});

test("global attention selects its owning run and returns to its conversation after answering", async ({
  page,
}) => {
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Other task";
  snapshot.inbox.push(second);
  snapshot.runs = [
    retained(snapshot, "attention-owner", "waiting"),
    { ...retained(snapshot, "other"), itemId: second.item.id },
  ];
  snapshot.attention = [
    {
      id: "global-question",
      runId: "attention-owner",
      method: "item/tool/requestUserInput",
      blocking: true,
      status: "pending",
      params: {
        questions: [
          {
            id: "scope",
            header: "Scope",
            question: "Choose scope",
            options: [{ label: "Focused", description: "One fix" }],
          },
        ],
      },
    },
  ];
  const { ui, calls } = await host(page, { snapshot });
  await openItem(ui, /Other task/);
  await expect(
    ui.getByRole("button", { name: "← Issues", exact: true }),
  ).toBeVisible();
  await openWaiting(ui);
  const dialog = ui.getByRole("dialog");
  await expect(dialog).toContainText(
    "Fix reconnect regression · Investigation",
  );
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "← Messages", exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "← Issues", exact: true }),
  ).toHaveCount(0);
  await expect(
    ui.getByRole("heading", {
      name: "Fix reconnect regression",
      exact: true,
      level: 1,
    }),
  ).toBeFocused();
  await expect(ui.getByRole("button", { name: /^Messages\s*1$/ })).toHaveCount(
    0,
  );
  await ui.getByLabel("Message agent").fill("Continue the owning run");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect
    .poll(() => calls.find((c) => c.name === "send_message")?.arguments.id)
    .toBe("attention-owner");
});

test("Messages lists waiting agents and answering removes only its owning request", async ({
  page,
}) => {
  const snapshot = initial();
  const second = structuredClone(snapshot.inbox[0]);
  second.item.id = "owner/repo#2";
  second.item.title = "Second task";
  snapshot.inbox.push(second);
  snapshot.runs = [
    retained(snapshot, "one", "waiting"),
    { ...retained(snapshot, "two", "waiting"), itemId: second.item.id },
  ];
  snapshot.attention = snapshot.runs.map((run, i) => ({
    id: `question-${i}`,
    runId: run.id,
    method: "item/tool/requestUserInput",
    blocking: true,
    status: "pending",
    params: {
      questions: [
        {
          id: "scope",
          header: "Scope",
          question: `Scope for task ${i}?`,
          options: [{ label: "Focused", description: "Small fix" }],
        },
      ],
    },
  }));
  const request = snapshot.attention[0];
  snapshot.runs.push({
    ...retained(snapshot, "disconnected", "interrupted"),
    itemId: second.item.id,
  });
  snapshot.attention.push(
    { ...structuredClone(request), id: "nonblocking", blocking: false },
    { ...structuredClone(request), id: "answered", status: "answered" },
    { ...structuredClone(request), id: "stale", status: "stale" },
    { ...structuredClone(request), id: "orphaned", runId: "missing-run" },
    {
      ...structuredClone(request),
      id: "disconnected-request",
      runId: "disconnected",
    },
    {
      ...structuredClone(request),
      id: "approval",
      method: "item/commandExecution/requestApproval",
      params: {
        command: "git status",
        reason: "Inspect changes",
        availableDecisions: ["decline"],
      },
    },
  );
  const { ui, calls } = await host(page, { snapshot });
  await ui.getByRole("button", { name: /^Messages/ }).click();
  const waiting = ui.getByRole("region", { name: "Waiting for you" });
  await expect(waiting.getByRole("button")).toHaveCount(4);
  await expect(
    ui.getByRole("button", { name: /^Messages\s*4$/ }),
  ).toBeVisible();
  await expect(ui.getByText("All agents", { exact: true })).toHaveCount(0);
  await expect(ui.getByText("Work history", { exact: true })).toHaveCount(0);
  await waiting.getByRole("button", { name: /Second task/ }).click();
  await expect(ui.getByRole("dialog")).toContainText(
    "Second task · Investigation",
  );
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(
    ui.getByRole("heading", { name: "Second task", exact: true }),
  ).toBeVisible();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(3);
  await expect(waiting).toContainText("Fix reconnect regression");
  expect(calls.find((c) => c.name === "answer_request")!.arguments.id).toBe(
    "question-1",
  );
  await waiting.getByRole("button", { name: /Approval needed/ }).click();
  await expect(ui.getByRole("dialog")).toContainText("git status");
  await ui.getByRole("button", { name: "Deny", exact: true }).click();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(2);
  expect(
    calls.filter((c) => c.name === "answer_request").at(-1)?.arguments,
  ).toEqual({ id: "approval", answer: { decision: "decline" } });
  await waiting.getByRole("button", { name: /Reply needed/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(1);
  await waiting.getByRole("button", { name: /agent continues/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await ui.getByRole("button", { name: "← Messages", exact: true }).click();
  await expect(waiting.getByRole("button")).toHaveCount(0);
  await expect(waiting).toContainText("No messages waiting for you.");
  await expect(
    ui.getByRole("button", { name: "Messages", exact: true }),
  ).toBeVisible();
});

test("an uncertain textual reply can be dismissed without advancing its session", async ({
  page,
}) => {
  const state = initial();
  const run = retained(state, "retained", "idle");
  state.runs = [run];
  state.attention = [
    {
      id: "uncertain",
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
            question: "The agent's final reply",
            isOther: true,
          },
        ],
      },
    },
  ];
  const { ui, calls } = await host(page, { snapshot: state });
  await ui.getByRole("button", { name: /Messages/ }).click();
  await ui
    .getByRole("region", { name: "Waiting for you" })
    .getByRole("button")
    .click();
  await ui
    .getByRole("button", { name: "No reply needed", exact: true })
    .click();
  await expect(ui.getByRole("dialog")).toHaveCount(0);
  expect(
    calls.filter((c) => c.name === "dismiss_question").map((c) => c.arguments),
  ).toEqual([{ id: "uncertain" }]);
  expect(
    calls.filter((c) =>
      ["start_run", "send_message", "answer_request"].includes(c.name),
    ),
  ).toHaveLength(0);
  expect(run.status).toBe("idle");
});

test("clarification progress has no attention badge and stops when unresolved", async ({ page }) => {
  const snapshot = initial();
  const run = retained(snapshot, "clarifying", "idle");
  run.turnId = "";
  snapshot.runs = [run];
  snapshot.questionRecoveries = [{ id: "clarify-1", runId: run.id, stage: "clarifying", attempts: 1 }];
  const { ui, state, calls } = await host(page, { snapshot });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  const progress = ui.getByRole("region", { name: "Question recovery" });
  await expect(progress).toContainText("Clarifying whether input is needed · attempt 1 of 2");
  await expect(ui.getByRole("button", { name: "Take over session", exact: true })).toBeVisible();
  await expect(ui.getByRole("button", { name: "Messages", exact: true })).toBeVisible();
  await expect(ui.getByRole("button", { name: /Waiting for your answer/ })).toHaveCount(0);
  await expect(ui.locator(".badge").filter({ hasText: "Needs attention" })).toHaveCount(0);
  // Filled draft proves that recovery, rather than empty-input validation, blocks Send.
  await ui.getByLabel("Message agent").fill("Owner direction");
  const send = ui.getByRole("button", { name: "Send message", exact: true });
  await expect(send).toBeDisabled();
  state.questionRecoveries![0].stage = "unresolved";
  state.questionRecoveries![0].attempts = 2;
  state.cursor++;
  await expect(progress).toHaveCount(0);
  await expect(ui.getByRole("button", { name: "Take over session", exact: true })).toHaveCount(0);
  await expect(ui.getByRole("button", { name: "Messages", exact: true })).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toHaveValue("Owner direction");
  await expect(send).toBeEnabled();
  expect(calls.filter((c) => c.name === "send_message" || c.name === "start_run")).toHaveLength(0);
});

test("nonblocking recovered text question keeps its continuing label", async ({ page }) => {
  const snapshot = initial();
  const run = retained(snapshot, "nonblocking", "idle");
  snapshot.runs = [run];
  snapshot.attention = [{ id: "nonblocking-text", runId: run.id, origin: "text", method: "item/tool/requestUserInput", blocking: false, status: "pending", params: { questions: [{id: "reply", question: "Which target?", isOther: true}] } }];
  const { ui } = await host(page, { snapshot });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByRole("button", { name: "Question · agent continues — Answer" })).toBeVisible();
  await expect(ui.getByRole("button", { name: /Waiting for your answer/ })).toHaveCount(0);
});

test("a hidden older read cannot consume newer run and attention invalidations",async({page})=>{
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
    state.runs=[retained(state,"new-hidden-question","waiting")];
    state.attention=[{id:"hidden-question",runId:"new-hidden-question",status:"pending",method:"item/tool/requestUserInput",blocking:true,params:{questions:[{id:"scope",question:"Which scope?",options:[{label:"Focused"},{label:"Broad"}]}]}} as Snapshot["attention"][number]];
    state.inbox[0].item.title="Fresh hidden state";state.cursor=101;
    await expect.poll(()=>calls.some(c=>c.name==="watch_ui_changes"&&c.arguments.after===101)).toBe(true);
    release();await expect.poll(()=>returned).toBe(true);
    await ui.locator("body").evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
    await page.locator("iframe").evaluate((frame:HTMLIFrameElement)=>{
      Object.defineProperty(frame.contentDocument!,"hidden",{configurable:true,get:()=>false});
      frame.contentDocument!.dispatchEvent(new Event("visibilitychange"));
    });
    await ui.getByLabel("Search inbox").fill("");
    await expect(ui.getByRole("button",{name:/Fresh hidden state/})).toBeVisible();
    await expect(ui.getByRole("button",{name:"Messages 1",exact:true})).toBeVisible();
  }finally{release()}
});
