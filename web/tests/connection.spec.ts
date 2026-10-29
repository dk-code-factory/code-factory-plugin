import { test, expect } from "@playwright/test";
import { initial, host, retained } from "./fixtures";

test("initial bridge failures retry a fresh handshake without reloading or losing state", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { initializeFailures: 3 });
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Connected");
  expect(
    await page.evaluate(
      () =>
        (window as unknown as { initializationCount: number })
          .initializationCount,
    ),
  ).toBe(4);
  expect(calls.filter((c) => c.name === "read_ui_state")).toHaveLength(1);
  await expect(ui.getByRole("alert")).toHaveCount(0);
});

test("mutation connection failure is not replayed and later polling does not dismiss it", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "send-once")];
  const { ui, calls } = await host(page, {
    snapshot,
    transportFault: (name) => name === "send_message",
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui
    .getByLabel("Message agent")
    .fill("Send only once, retaining the uncertain draft.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Connection to Code Factory interrupted",
  );
  const polls = calls.filter((c) => c.name === "watch_ui_changes").length;
  await expect
    .poll(() => calls.filter((c) => c.name === "watch_ui_changes").length)
    .toBeGreaterThan(polls + 1);
  expect(calls.filter((c) => c.name === "send_message")).toHaveLength(1);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Send only once, retaining the uncertain draft.",
  );
  await expect(ui.getByRole("alert")).toContainText(
    "Connection to Code Factory interrupted",
  );
});

test("read tool errors are not mistaken for retryable transport errors", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    toolError: {
      name: "read_item",
      message: "Connection failed while validating this item.",
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Connection failed while validating this item",
  );
  await expect(
    ui.getByRole("button", { name: "Retry loading item", exact: true }),
  ).toBeVisible();
  const polls = calls.filter((c) => c.name === "watch_ui_changes").length;
  await expect
    .poll(() => calls.filter((c) => c.name === "watch_ui_changes").length)
    .toBeGreaterThan(polls + 1);
  expect(calls.filter((c) => c.name === "read_item")).toHaveLength(1);
});

test("exact host-wrapped MCP connection error retries a read without exposing its framework text", async ({
  page,
}) => {
  const failure = {
    name: "unused",
    message: "McpServerError: Connection failed.",
  };
  const { ui, calls } = await host(page, { toolError: failure });
  await ui.locator("main").evaluate((root) => {
    root.dataset.frameworkErrorSeen = "false";
    new MutationObserver(() => {
      if (
        [...root.querySelectorAll('[role="alert"]')].some((e) =>
          e.textContent?.includes("McpServerError"),
        )
      )
        root.dataset.frameworkErrorSeen = "true";
    }).observe(root, { subtree: true, childList: true, characterData: true });
  });
  failure.name = "watch_ui_changes";
  await expect
    .poll(() => calls.filter((c) => c.name === "watch_ui_changes").length)
    .toBeGreaterThan(0);
  failure.name = "unused";
  await expect
    .poll(() => calls.filter((c) => c.name === "watch_ui_changes").length)
    .toBeGreaterThan(1);
  await expect(
    ui.getByRole("status", { name: "Connection status" }),
  ).toHaveText("Connected");
  await expect(ui.getByRole("alert")).toHaveCount(0);
  await expect(ui.locator("main")).toHaveAttribute(
    "data-framework-error-seen",
    "false",
  );
  expect(
    calls.some((c) =>
      ["start_run", "send_message", "answer_request", "save_settings"].includes(
        c.name,
      ),
    ),
  ).toBe(false);
});

test("exact host-wrapped connection failure never replays a mutation", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "wrapped-send")];
  const { ui, calls } = await host(page, {
    snapshot,
    toolError: {
      name: "send_message",
      message: "McpServerError: Connection failed.",
    },
  });
  await ui.getByRole("button", { name: /Fix reconnect regression/ }).click();
  await ui.getByLabel("Message agent").fill("Retain this uncertain message.");
  await ui.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(ui.getByRole("alert")).toContainText(
    "Connection to Code Factory interrupted",
  );
  const polls = calls.filter((c) => c.name === "watch_ui_changes").length;
  await expect
    .poll(() => calls.filter((c) => c.name === "watch_ui_changes").length)
    .toBeGreaterThan(polls + 1);
  expect(calls.filter((c) => c.name === "send_message")).toHaveLength(1);
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Retain this uncertain message.",
  );
});
