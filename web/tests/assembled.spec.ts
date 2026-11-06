import { test, expect } from "@playwright/test";
import { host } from "./wrapper-fixtures";

test("assembled native session retains thread across question answer and follow-up", async ({
  page,
}) => {
  const { ui, calls, forbidden } = await host(page, { assembled: true });
  await ui.getByRole("button", { name: /Fresh first message fixture/ }).click();
  expect(calls.some((c) => c.name === "session_chat")).toBe(false);
  await ui.getByLabel("Message", { exact: true }).fill("Inspect the fixture");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("radio", { name: /Focused/ })).toBeVisible();
  await ui.getByRole("radio", { name: /Focused/ }).check();
  await ui.getByRole("button", { name: "Send answer" }).click();
  await expect(ui.getByLabel("Conversation transcript")).toContainText(
    "Answer received. Task complete.",
  );
  await expect(ui.getByLabel("Session status")).toContainText(
    "Execution: idle",
  );
  await expect(ui.getByLabel("Session status")).toContainText(
    "Reported status: idle",
  );
  await ui
    .getByLabel("Message", { exact: true })
    .fill("Follow up in the same conversation");
  await ui.getByRole("button", { name: "Send", exact: true }).click();
  await expect(ui.getByRole("radio", { name: /Focused/ })).toBeVisible();
  expect(calls.filter((c) => c.name === "session_chat")).toHaveLength(2);
  expect(forbidden).toEqual([]);
});
