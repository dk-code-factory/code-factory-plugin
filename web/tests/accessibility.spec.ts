import { test, expect } from "@playwright/test";
import type { Snapshot } from "../src/types";
import { initial, host, showSettings, openItem, showItemDetails, linkTarget, triage, tabTo, scanOwnedApp, retained } from "./fixtures";

for (const theme of ["light", "dark"] as const) {
  test(`Axe WCAG A/AA scans the owned app iframe across core states in ${theme} theme`, async ({
    page,
  }) => {
    const { ui } = await host(page, { theme });
    await expect(ui.locator("html")).toHaveAttribute("data-theme", theme);
    // Work can inject this after the app stylesheet. Keep the host's transparent
    // document while painting the owned application surfaces in the host theme.
    await ui.locator("html").evaluate((html) => {
      const style = html.ownerDocument.createElement("style");
      // Force the observed host result despite Vite's higher-specificity :root
      // stylesheet; the regression is transparent html with themed text.
      style.textContent = "html { background-color: transparent !important; }";
      html.ownerDocument.head.append(style);
    });
    await expect(ui.locator("html")).toHaveCSS(
      "background-color",
      "rgba(0, 0, 0, 0)",
    );
    const background =
      theme === "dark" ? "rgb(23, 32, 28)" : "rgb(246, 248, 245)";
    for (const selector of ["body", "#root", "main"]) {
      await expect(ui.locator(selector)).toHaveCSS(
        "background-color",
        background,
      );
    }
    await scanOwnedApp(page, `${theme}-inbox`);
    await openItem(ui, /Fix reconnect regression/);
    await (await linkTarget(ui)).fill("owner/repo#42");
    await ui
      .getByRole("button", { name: "Add local link", exact: true })
      .click();
    await expect(
      ui.locator(".detail p").filter({ hasText: "owner/repo#42" }),
    ).toBeVisible();
    await triage(ui, "Record action");
    await ui.getByText("Your action history", { exact: true }).click();
    await expect(
      ui.locator(".detail p").filter({ hasText: /^plugin ·/ }),
    ).toBeVisible();
    await scanOwnedApp(page, `${theme}-detail`);
    await ui.getByRole("button", { name: "New run", exact: true }).click();
    await expect(
      ui.getByRole("dialog", { name: "Launch agent" }),
    ).toBeVisible();
    await ui.getByRole("button", { name: "Edit", exact: true }).click();
    await scanOwnedApp(page, `${theme}-launch`);
    await ui
      .getByRole("button", { name: "Review launch", exact: true })
      .click();
    await expect(
      ui.getByRole("heading", { name: "Review agent launch" }),
    ).toBeVisible();
    await scanOwnedApp(page, `${theme}-review`);
    await ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true })
      .click();
    await expect(ui.getByText("Inspecting reconnect logic")).toBeVisible();
    await scanOwnedApp(page, `${theme}-agents`);
    await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
    await ui.getByRole("radio", { name: /Focused/ }).check();
    await expect(
      ui.getByRole("button", { name: "Answer", exact: true }),
    ).toBeEnabled();
    await scanOwnedApp(page, `${theme}-question`);
    await page.keyboard.press("Escape");
    await showSettings(page);
    await expect(
      ui.getByText("Connection diagnostics", { exact: true }),
    ).toBeVisible();
    for (const section of [
      "Priorities & notifications",
      "Launch profiles",
      "Managed skills",
      "Connection diagnostics",
    ])
      await ui.getByText(section, { exact: true }).click();
    await scanOwnedApp(page, `${theme}-settings`);
  });
}

test("keyboard launch review traps focus, preserves edits and restores its opener on Escape", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  await tabTo(
    page,
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  );
  await page.keyboard.press("Enter");
  await showItemDetails(ui);
  const opener = ui.getByRole("button", { name: "New run", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  const close = dialog.getByRole("button", { name: "Close launch" });
  await expect(close).toBeFocused();
  await expect(close).toHaveCSS("outline-style", "solid");
  await expect(close).toHaveCSS("outline-width", "3px");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
  await tabTo(page, dialog.getByRole("button", { name: "Edit", exact: true }));
  await page.keyboard.press("Enter");
  const prompt = dialog.getByLabel("Task prompt");
  await tabTo(page, prompt);
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("Keep this keyboard launch draft.");
  await expect(prompt).toHaveCSS("outline-width", "3px");
  const snapshots = calls.filter(
    (call) => call.name === "read_ui_state",
  ).length;
  state.cursor++;
  await expect
    .poll(() => calls.filter((call) => call.name === "read_ui_state").length)
    .toBeGreaterThan(snapshots);
  await expect(prompt).toBeFocused();
  await expect(prompt).toHaveValue("Keep this keyboard launch draft.");
  await tabTo(
    page,
    dialog.getByRole("button", { name: "Review launch", exact: true }),
  );
  await page.keyboard.press("Enter");
  await expect(
    ui.getByRole("heading", { name: "Review agent launch" }),
  ).toBeVisible();
  await expect(dialog).toContainText("read-only · on-request");
  await expect(dialog).toContainText("Ask before publishing");
  await expect(close).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "Start agent", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
  expect(calls.filter((call) => call.name === "prepare_launch")).toHaveLength(
    2,
  );
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(0);
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toContainText("Connected");
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveAttribute("aria-live", "polite");
});

test("keyboard attention stays contained through updates and Escape restores the pending opener", async ({
  page,
}) => {
  const { ui, state, calls } = await host(page);
  await tabTo(
    page,
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  );
  await page.keyboard.press("Enter");
  await showItemDetails(ui);
  await tabTo(page, ui.getByRole("button", { name: "New run", exact: true }));
  await page.keyboard.press("Enter");

  await tabTo(
    page,
    ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true }),
  );
  await page.keyboard.press("Enter");
  const opener = ui.getByRole("button", { name: /Waiting for your answer/ });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Agent needs attention" });
  const close = dialog.getByRole("button", { name: "Close attention" });
  await expect(close).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(
    dialog.getByRole("button", { name: "Open agent chat" }),
  ).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(close).toBeFocused();
  await page.keyboard.press("Tab");
  const focused = dialog.getByRole("radio", { name: /Focused/ });
  await expect(focused).toBeFocused();
  await page.keyboard.press("Space");
  await expect(focused).toBeChecked();
  await expect(focused).toHaveCSS("outline-width", "3px");
  state.attention[0].params.questions![0].options![0].description =
    "Updated focused guidance";
  state.cursor++;
  await expect(dialog.getByText("Updated focused guidance")).toBeVisible();
  await expect(focused).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
  expect(calls.filter((call) => call.name === "answer_request")).toHaveLength(
    0,
  );
  await page.keyboard.press("Enter");
  await expect(close).toBeFocused();
  await page.keyboard.press("Tab");
  await page.keyboard.press("Space");
  await tabTo(
    page,
    dialog.getByRole("button", { name: "Answer", exact: true }),
  );
  await page.keyboard.press("Enter");
  await expect(dialog).toHaveCount(0);
  expect(calls.filter((call) => call.name === "answer_request")).toHaveLength(
    1,
  );
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveText(
    "Inactive",
  );
  await expect(ui.getByRole("status", { name: "Work status" })).toHaveAttribute(
    "aria-live",
    "polite",
  );
});

test("launch preparation failure is announced inside the dialog without losing keyboard edits", async ({
  page,
}) => {
  const options = { failPreparation: true };
  const { ui, calls } = await host(page, options);
  await tabTo(
    page,
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  );
  await page.keyboard.press("Enter");
  await showItemDetails(ui);
  const opener = ui.getByRole("button", { name: "New run", exact: true });
  await tabTo(page, opener);
  await page.keyboard.press("Enter");
  const dialog = ui.getByRole("dialog", { name: "Launch agent" });
  await expect(dialog.getByRole("alert")).toContainText(
    "Fixture preparation denied",
  );
  options.failPreparation = false;
  await dialog.getByRole("button", { name: "Retry preparation" }).click();
  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  options.failPreparation = true;
  await tabTo(page, dialog.getByLabel("Task prompt"));
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("Retain this failed preparation draft.");
  await tabTo(
    page,
    dialog.getByRole("button", { name: "Review launch", exact: true }),
  );
  await page.keyboard.press("Enter");
  await expect(dialog.getByRole("alert")).toContainText(
    "Fixture preparation denied",
  );
  await expect(dialog.getByLabel("Task prompt")).toHaveValue(
    "Retain this failed preparation draft.",
  );
  await expect(
    dialog.getByRole("button", { name: "Review launch", exact: true }),
  ).toBeEnabled();
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(0);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test("page changes keep keyboard focus usable when an unread row leaves the list", async ({
  page,
}) => {
  const { ui } = await host(page);
  await ui.getByLabel("Inbox filter").selectOption("unread");
  await openItem(ui, /Fix reconnect regression/);
  await ui.getByRole("button", { name: "← Issues", exact: true }).click();
  await expect(
    ui.getByText("No matching items", { exact: true }),
  ).toBeVisible();
  await expect(ui.locator(".detail")).toHaveCount(0);
  await ui.getByRole("button", { name: "Clear filters", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Fix reconnect regression/ }),
  ).toBeVisible();
});

for (const theme of ["light", "dark"] as const) {
  test(`focused work queues use GitHub ownership review requests and pending priority in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 820 });
    const snapshot = initial();
    const make = (
      number: number,
      kind: string,
      priority: number | null,
      options: Partial<Snapshot["inbox"][number]["item"]> = {},
    ) => {
      const row = structuredClone(snapshot.inbox[0]);
      row.item = {
        ...row.item,
        id: `owner/repo#${number}`,
        number,
        kind,
        title: `Work ${number}`,
        url: `https://github.com/owner/repo/${kind === "pr" ? "pull" : "issues"}/${number}`,
        ...options,
      };
      row.priority = priority;
      return row;
    };
    const linked = make(4, "issue", 0);
    linked.hasPullRequest = true;
    snapshot.inbox = [
      snapshot.inbox[0],
      make(2, "pr", 1),
      make(3, "pr", 2, { author: "other", reviewRequests: ["alex"] }),
      linked,
      make(5, "issue", 0, { state: "closed" }),
      make(6, "issue", null),
      make(7, "issue", 0),
      make(8, "pr", 0, { author: "other", reviewTeams: ["org/core"] }),
      make(9, "pr", 1, { author: "other" }),
      make(10, "pr", 0, { state: "closed" }),
    ];
    snapshot.viewer!.teams = ["org/core"];
    const { ui, calls } = await host(page, { snapshot, theme });
    const rows = ui.getByRole("region", { name: "Items" }).getByRole("button");
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(rows).toHaveCount(4);
    await ui.getByLabel("Inbox filter").selectOption("pending");
    await expect(rows).toHaveText([
      /Work 7/,
      /Fix reconnect regression/,
      /Work 6/,
    ]);
    await ui.getByLabel("Inbox filter").selectOption("open");
    await expect(rows).toHaveCount(4);
    await ui.getByLabel("Search inbox").fill("Work 4");
    await ui.getByRole("button", { name: "My PRs", exact: true }).click();
    await expect(rows).toHaveText([/Work 2/]);
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(ui.getByLabel("Search inbox")).toHaveValue("");
    await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
    await expect(rows).toHaveText([/Work 8/, /Work 3/]);
    await ui.getByRole("button", { name: "Issues", exact: true }).click();
    await expect(ui.getByLabel("Search inbox")).toHaveValue("Work 4");
    await expect(ui.getByLabel("Inbox filter")).toHaveValue("open");
    await expect(rows).toHaveText([/Work 4/]);
    await expect(ui.locator("body")).toHaveJSProperty(
      "scrollWidth",
      await ui.locator("body").evaluate((e) => e.clientWidth),
    );
    expect(
      calls.some((call) =>
        ["read_item", "start_run", "prepare_launch"].includes(call.name),
      ),
    ).toBe(false);
    await page.screenshot({
      path: test.info().outputPath(`work-queues-${theme}.png`),
    });
    await scanOwnedApp(page, `work-queues-${theme}`);
  });
}

test("question recovery progress and keyboard takeover retain the session draft", async ({
  page,
}) => {
  const state = initial();
  const run = retained(state, "repairing", "running");
  run.turnId = "repair-turn";
  state.runs = [run];
  state.questionRecoveries = [
    { id: "recovery", runId: run.id, stage: "requesting-form", attempts: 2 },
  ];
  const { ui, calls } = await host(page, { snapshot: state });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(
    ui.getByRole("region", { name: "Question recovery" }),
  ).toContainText("attempt 2 of 2");
  await ui.getByLabel("Message agent").fill("Retain takeover context");
  const takeover = ui.getByRole("button", {
    name: "Take over session",
    exact: true,
  });
  await takeover.focus();
  await takeover.press("Enter");
  await expect(ui.getByLabel("Session console")).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Retain takeover context",
  );
  await expect
    .poll(() =>
      calls.filter((c) => c.name === "interrupt_run").map((c) => c.arguments),
    )
    .toEqual([{ id: run.id }]);
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`reported issues navigation remains keyboard reachable at narrow width in ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const { ui, calls } = await host(page, { theme });
    const reports = ui.getByRole("button", { name: "Reported issues", exact: true });
    await tabTo(page, reports);
    await page.keyboard.press("Enter");
    await expect(ui.getByRole("heading", { name: "Reported issues", exact: true })).toBeVisible();
    await expect(reports).toHaveAttribute("aria-current", "page");
    expect(await ui.locator("body").evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    expect(calls.some(c => ["start_run", "refresh_issue_reports"].includes(c.name))).toBe(false);
    await scanOwnedApp(page, `reports-navigation-${theme}`);
  });
}
