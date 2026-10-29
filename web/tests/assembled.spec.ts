import { test, expect } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import type { Run } from "../src/types";
import { showItemDetails, retained, assembledHost } from "./fixtures";

test("lost Start response locates the accepted daemon run without another Start", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui, calls, acceptedStarts, forbidden, daemon } = await assembledHost(
    page,
    {
      loseFirstStartResponse: true,
      beforeFirstStartResponse: () => gate,
    },
  );
  const before = await daemon<Run[]>("list_runs");
  try {
    await ui.getByRole("button", { name: /Assembled daemon fixture/ }).click();
    await showItemDetails(ui);
    await ui.getByRole("button", { name: /^New run$/, exact: true }).click();
    await expect(
      ui
        .getByRole("dialog", { name: "Launch agent" })
        .getByRole("button", { name: "Start agent", exact: true }),
    ).toBeEnabled();
    const dialog = ui.getByRole("dialog", { name: "Launch agent" });
    const start = ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true });
    await start.dblclick();
    await expect(start).toBeDisabled();
    await expect
      .poll(() => calls.filter((call) => call.name === "start_run").length)
      .toBe(1);
    release();
    await expect(dialog).toHaveCount(0);
    expect(acceptedStarts).toHaveLength(1);
    const created = (await daemon<Run[]>("list_runs")).filter(
      (run) => !before.some((old) => old.id === run.id),
    );
    expect(created).toHaveLength(1);
    expect(created[0].id).toBe(acceptedStarts[0].id);
    expect(
      calls
        .filter((call) => call.name === "start_run")
        .map((call) => call.arguments),
    ).toEqual([{ id: created[0].id }]);
    expect(
      calls.some(
        (call) =>
          call.name === "read_run" && call.arguments.id === created[0].id,
      ),
    ).toBe(true);
    await expect(
      ui.getByRole("button", { name: /Waiting for your answer/ }),
    ).toBeVisible();
    expect(calls.filter((call) => call.name === "prepare_launch")).toHaveLength(
      1,
    );
    expect(forbidden).toEqual([]);
  } finally {
    release();
  }
});

test("sending a message restores the dirty retained daemon session without a Resume action", async ({
  page,
}) => {
  const { ui, calls, acceptedStarts, forbidden, daemon } =
    await assembledHost(page);
  await ui.getByRole("button", { name: /Assembled daemon fixture/ }).click();
  await showItemDetails(ui);
  await ui.getByRole("button", { name: /^New run$/, exact: true }).click();
  await expect(
    ui
      .getByRole("dialog", { name: "Launch agent" })
      .getByRole("button", { name: "Start agent", exact: true }),
  ).toBeEnabled();
  await ui
    .getByRole("dialog", { name: "Launch agent" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await daemon<Run[]>("list_runs")).find(
          (r) => r.id === acceptedStarts[0].id,
        )?.status,
    )
    .toBe("idle");
  const run = (await daemon<Run[]>("list_runs")).find(
    (entry) => entry.id === acceptedStarts[0].id,
  )!;
  expect(relative(tmpdir(), run.worktree)).toMatch(
    /^factory-browser-[^/]+\/worktrees\/[a-f0-9]+$/,
  );
  const sentinel = join(run.worktree, "browser-dirty-fixture.txt");
  await writeFile(sentinel, "retain this browser fixture change\n");
  await ui.getByLabel("Message agent").fill("Retain this follow-up draft.");
  await ui.getByText("Details", { exact: true }).click();
  await ui
    .getByRole("button", { name: "Clean up worktree", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toContainText(
    /modified or untracked files|worktree/,
  );
  await expect
    .poll(
      async () =>
        (await daemon<Run[]>("list_runs")).find((r) => r.id === run.id)?.status,
    )
    .toBe("interrupted");
  await expect(
    ui.getByRole("button", { name: "Resume agent", exact: true }),
  ).toHaveCount(0);
  await expect(
    ui.getByRole("button", { name: "Send message", exact: true }),
  ).toBeEnabled();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Retain this follow-up draft.",
  );
  await expect(
    ui.getByText("Answer received. Task complete.", { exact: false }),
  ).toBeVisible();
  expect(await readFile(sentinel, "utf8")).toBe(
    "retain this browser fixture change\n",
  );
  const interrupted = (await daemon<Run[]>("list_runs")).find(
    (entry) => entry.id === run.id,
  )!;
  expect(interrupted.status).toBe("interrupted");
  expect(interrupted.worktree).toBe(run.worktree);
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(1);
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(
    ui.getByRole("button", { name: /Waiting for your answer/ }),
  ).toBeVisible();
  await expect(ui.locator(".detail .status")).toHaveText("Waiting for you");
  const resumed = (await daemon<Run[]>("list_runs")).find(
    (entry) => entry.id === run.id,
  )!;
  expect(resumed.worktree).toBe(run.worktree);
  expect(resumed.threadId).toBe(run.threadId);
  expect(await readFile(sentinel, "utf8")).toBe(
    "retain this browser fixture change\n",
  );
  await expect(
    ui.getByText("Answer received. Task complete.", { exact: false }),
  ).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toHaveValue("");
  expect(
    calls
      .filter((call) => call.name === "send_message")
      .map((call) => call.arguments),
  ).toEqual([
    { id: run.id, text: "Retain this follow-up draft.", expectedTurn: "" },
  ]);
  expect(
    calls
      .filter((call) => call.name === "start_run")
      .map((call) => call.arguments),
  ).toEqual([{ id: run.id }]);
  expect(forbidden).toEqual([]);
});

test("MCP bridge drives the assembled daemon through launch, question, answer and follow-up", async ({
  page,
}) => {
  const { ui, calls, forbidden } = await assembledHost(page);
  await ui.getByRole("button", { name: /Fresh first message fixture/ }).click();
  expect(calls.map((call) => call.name)).not.toContain("start_run");
  await expect(ui.getByLabel("Message agent")).not.toHaveValue("");
  await ui
    .getByLabel("Message agent")
    .fill("Inspect the local fixture and ask which scope to investigate.");
  await ui
    .getByRole("region", { name: "New agent conversation" })
    .getByRole("button", { name: "Start agent", exact: true })
    .click();
  await ui.getByRole("button", { name: /Waiting for your answer/ }).click();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(ui.getByRole("dialog")).toHaveCount(0);
  await expect(
    ui.getByText("Answer received. Task complete.", { exact: false }),
  ).toBeVisible();
  await ui
    .getByLabel("Message agent")
    .fill("Follow-up on the isolated fixture.");
  await ui.getByRole("button", { name: "Send message" }).click();
  await expect
    .poll(() => calls.filter((call) => call.name === "send_message").length)
    .toBe(1);
  expect(calls.filter((call) => call.name === "start_run")).toHaveLength(1);
  expect(forbidden).toEqual([]);
});
