import { test, expect } from "@playwright/test";
import { initial, host, showSettings, openItem, showItemDetails, showAgents, linkTarget, scanOwnedApp, retained } from "./fixtures";

test("settings save repository and priority rows without unused integrations", async ({
  page,
}) => {
  const { ui, calls, state } = await host(page, { surface: "settings" });
  await expect(ui.getByLabel("Assessment prompt")).toBeHidden();
  await ui.getByLabel("GitHub repository").fill("owner/repo");
  await ui
    .getByLabel("Existing checkout (optional)")
    .fill("/home/user/project");
  await ui
    .getByRole("button", { name: "Save repository", exact: true })
    .click();
  await expect(
    ui.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Issues", exact: true }),
  ).toHaveCount(0);
  await expect(
    ui.getByRole("button", { name: "Edit repository owner/repo", exact: true }),
  ).toBeVisible();
  await expect(ui.getByText("Integrations", { exact: true })).toHaveCount(0);
  await expect(ui.getByLabel("Jira API token")).toHaveCount(0);
  expect(calls.filter((c) => c.name === "set_jira_credential")).toHaveLength(0);
  await ui.getByText("Priorities & notifications", { exact: true }).click();
  await ui.getByLabel("Priority label 1", { exact: true }).fill("urgent");
  await ui.getByLabel("Mapped priority 1", { exact: true }).selectOption("0");
  await ui
    .getByLabel("Question helper model (optional)")
    .fill("fixture-small-model");
  await ui.getByRole("button", { name: "Save settings", exact: true }).click();
  const form = ui
    .getByRole("button", { name: "Save settings", exact: true })
    .locator("xpath=ancestor::form");
  await expect(form.getByRole("status")).toHaveText("Saved.");
  expect(
    calls.find((c) => c.name === "save_settings")?.arguments.priorityLabels,
  ).toEqual({ urgent: 0, P1: 1, P2: 2, P3: 3 });
  expect(
    calls.find((c) => c.name === "save_settings")?.arguments
      .questionHelperModel,
  ).toBe("fixture-small-model");
  await ui.getByText("System defaults", { exact: true }).click();
  await ui.getByLabel("Request budget per hour", { exact: true }).fill("120");
  await ui
    .getByRole("button", { name: "Save system defaults", exact: true })
    .click();
  const systemForm = ui
    .getByRole("button", { name: "Save system defaults", exact: true })
    .locator("xpath=ancestor::form");
  await expect(systemForm.getByRole("status")).toHaveText("Saved.");
  const settingsCalls = calls.filter((c) => c.name === "save_settings");
  expect(settingsCalls).toHaveLength(2);
  expect(settingsCalls[1].arguments).toMatchObject({
    priorityLabels: { urgent: 0, P1: 1, P2: 2, P3: 3 },
    assessmentPrompt: "Assess urgency.",
    questionHelperModel: "fixture-small-model",
    githubRequestsPerHour: 120,
  });
  expect(state.config.priorityLabels).toEqual({ urgent: 0, P1: 1, P2: 2, P3: 3 });
  expect(state.config.githubRequestsPerHour).toBe(120);
});

test("repository form shows saving and errors beside retained inputs", async ({
  page,
}) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { ui } = await host(page, {
    surface: "settings",
    failSave: true,
    beforeSave: () => gate,
  });
  await ui.getByLabel("GitHub repository").fill("owner/repo");
  const form = ui
    .getByRole("button", { name: "Save repository", exact: true })
    .locator("xpath=ancestor::form");
  try {
    await form.getByRole("button",{name:"Save repository",exact:true}).click();
    await expect(form.getByRole("status")).toHaveText("Saving…");
    await expect(form.getByRole("button",{name:"Save repository",exact:true})).toBeDisabled();
    release();
    await expect(form.getByRole("alert")).toContainText(
      "Repository unavailable",
    );
    await expect(ui.getByLabel("GitHub repository")).toHaveValue("owner/repo");
    await expect(form.getByRole("button",{name:"Save repository",exact:true})).toBeEnabled();
  } finally {
    release();
  }
});

test("collapsed settings editors preserve capabilities and show independent save results", async ({
  page,
}) => {
  const { ui, calls } = await host(page, { surface: "settings" });
  await expect(ui.getByLabel("GitHub repository")).toBeFocused();
  for (const title of [
    "Launch profiles",
    "Managed skills",
    "Connection diagnostics",
  ])
    await ui.getByText(title, { exact: true }).click();
  const profileForm = ui
    .getByRole("button", { name: "Save profile", exact: true })
    .locator("xpath=ancestor::form");
  await profileForm.getByLabel("Name", { exact: true }).fill("Focused review");
  await profileForm
    .getByRole("combobox", { name: /Agent mode/ })
    .selectOption("plan");
  await profileForm.getByRole("button").click();
  await expect(profileForm.getByRole("status")).toHaveText("Saved.");
  expect(calls.find((c) => c.name === "save_profile")?.arguments).toMatchObject(
    {
      name: "Focused review",
      mode: "plan",
      sandbox: "read-only",
      publishing: "ask",
    },
  );
  const skillForm = ui
    .getByRole("button", { name: "Save skill", exact: true })
    .locator("xpath=ancestor::form");
  await skillForm
    .getByLabel("Skill name", { exact: true })
    .fill("focused-review");
  await skillForm.getByRole("button").click();
  await expect(skillForm.getByRole("status")).toHaveText("Saved.");
  expect(calls.find((c) => c.name === "save_skill")?.arguments.name).toBe(
    "focused-review",
  );
  const discovery = ui
    .getByRole("button", { name: "Discover local skills", exact: true })
    .locator("xpath=ancestor::form");
  await discovery.getByRole("button").click();
  await expect(discovery.getByRole("status")).toHaveText("Saved.");
  await ui
    .getByRole("button", { name: "Check connection", exact: true })
    .click();
  await expect(
    ui.getByText("verified · fixture-check: Fixture connection healthy"),
  ).toBeVisible();
  await expect(profileForm.getByRole("status")).toHaveText("Saved.");
});

for (const theme of ["light", "dark"] as const) {
  test(`settings has a dedicated ${theme} view without inbox or agent navigation`, async ({
    page,
  }) => {
    const { ui, calls } = await host(page, { surface: "settings", theme });
    await expect(
      ui.getByRole("heading", { name: "Settings", exact: true }),
    ).toBeVisible();
    await expect(ui.getByRole("navigation")).toHaveCount(0);
    await expect(
      ui.getByRole("button", { name: "Issues", exact: true }),
    ).toHaveCount(0);
    await expect(
      ui.getByRole("button", { name: "Agents", exact: true }),
    ).toHaveCount(0);
    await expect(ui.getByLabel("Search inbox")).toHaveCount(0);
    await expect(ui.getByLabel("Message agent")).toHaveCount(0);
    await ui.getByLabel("GitHub repository").fill("owner/repo");
    await ui
      .getByRole("button", { name: "Save repository", exact: true })
      .click();
    const form = ui
      .getByRole("button", { name: "Save repository", exact: true })
      .locator("xpath=ancestor::form");
    await expect(form.getByRole("status")).toHaveText("Saved.");
    await expect(
      ui.getByRole("heading", { name: "Settings", exact: true }),
    ).toBeVisible();
    expect(
      calls.filter((c) => ["prepare_launch", "start_run"].includes(c.name)),
    ).toHaveLength(0);
  });
}

test("inbox and agent views have no settings entry or configuration controls", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "no-settings")];
  const { ui } = await host(page, { snapshot });
  for (const tab of ["Issues", "My PRs", "Review PRs", "Messages"]) {
    await ui.getByRole("button", { name: tab, exact: true }).click();
    await expect(
      ui.getByRole("button", { name: "Settings", exact: true }),
    ).toHaveCount(0);
    await expect(ui.getByLabel("GitHub repository")).toHaveCount(0);
  }
  await showAgents(ui);
  await ui.locator(".work-history > .item").first().click();
  await expect(ui.getByLabel("Message agent")).toBeVisible();
  await expect(
    ui.getByRole("button", { name: "Settings", exact: true }),
  ).toHaveCount(0);
});

test("separate settings can save while the inbox view retains its item and agent drafts", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.runs = [retained(snapshot, "settings-concurrent")];
  const { ui, calls } = await host(page, { snapshot });
  await openItem(ui, /Fix reconnect regression/);
  await (await linkTarget(ui)).fill("owner/repo#kept");
  await ui.getByLabel("Message agent").fill("Keep this agent draft");
  await page.evaluate(() => {
    const frame = document.createElement("iframe");
    frame.title = "Code Factory settings";
    frame.src = "/settings-ui";
    frame.style.cssText = "width:100%;height:95vh;border:0";
    document.body.appendChild(frame);
  });
  const settings = page.frameLocator('iframe[title="Code Factory settings"]');
  await settings.getByLabel("GitHub repository").fill("owner/repo");
  await settings
    .getByRole("button", { name: "Save repository", exact: true })
    .click();
  await expect(
    settings
      .getByRole("button", { name: "Save repository", exact: true })
      .locator("xpath=ancestor::form")
      .getByRole("status"),
  ).toHaveText("Saved.");
  await expect(
    settings.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await expect(ui.getByLabel("Message agent")).toHaveValue(
    "Keep this agent draft",
  );
  await expect(
    ui.getByRole("button", { name: "Settings", exact: true }),
  ).toHaveCount(0);
  await showItemDetails(ui);
  await expect(ui.getByLabel("Link target")).toHaveValue("owner/repo#kept");
  expect(
    calls.filter((c) =>
      ["start_run", "send_message", "set_relationship"].includes(c.name),
    ),
  ).toHaveLength(0);
});

for (const theme of ["light", "dark"] as const) {
  test(`admin system and repository review policies retain inheritance and extra prompts in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const snapshot = initial();
    snapshot.profiles.push({
      ...snapshot.profiles[0],
      id: "team-review",
      name: "Team review",
    });
    const { ui, calls, state } = await host(page, {
      snapshot,
      surface: "settings",
      theme,
    });
    await ui.getByText("System defaults", { exact: true }).click();
    await expect(
      ui.getByLabel("System default for review requests"),
    ).toHaveValue("false");
    await ui
      .getByLabel("System default for review requests")
      .selectOption("true");
    await ui.getByLabel("System review pipeline").selectOption("team-review");
    await ui.getByLabel("Plugin update check interval").selectOption("60");
    await ui
      .getByRole("button", { name: "Save system defaults", exact: true })
      .click();
    await expect.poll(() => state.config.autoReview).toBe(true);
    await ui
      .getByLabel("GitHub repository", { exact: true })
      .fill("owner/repo");
    await expect(ui.getByLabel("Repository review requests")).toHaveValue(
      "inherit",
    );
    await ui.getByLabel("Repository review requests").selectOption("false");
    await ui
      .getByLabel("Repository review pipeline")
      .selectOption("team-review");
    await ui
      .getByLabel("Additional repository review prompt")
      .fill("Review transaction boundaries and preserve compatibility.");
    await ui
      .getByRole("button", { name: "Save repository", exact: true })
      .click();
    await expect.poll(() => state.repositories[0]?.autoReview).toBe(false);
    expect(
      calls.filter((c) => c.name === "save_repository").at(-1)?.arguments,
    ).toMatchObject({
      autoReview: false,
      reviewProfileId: "team-review",
      reviewPrompt: "Review transaction boundaries and preserve compatibility.",
    });
    await ui
      .getByRole("button", { name: "Edit repository owner/repo", exact: true })
      .click();
    await expect(
      ui.getByLabel("Additional repository review prompt"),
    ).toHaveValue("Review transaction boundaries and preserve compatibility.");
    await ui.getByLabel("Repository review requests").selectOption("inherit");
    await ui
      .getByRole("button", { name: "Save repository", exact: true })
      .click();
    await expect.poll(() => state.repositories[0]?.autoReview).toBeNull();
    await ui
      .getByRole("button", { name: "Poll repositories now", exact: true })
      .click();
    await expect
      .poll(() => calls.filter((c) => c.name === "sync_repositories").length)
      .toBe(1);
    expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
    await scanOwnedApp(page, `admin-repository-${theme}`);
  });
  test(`plugin admin tracks installs, versions and explicit updates in ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const { ui, calls } = await host(page, { surface: "settings", theme, memoizeReads: true });
    await ui.getByText("Plugins & skills", { exact: true }).click();
    const plugin = ui.locator(".plugin-row").filter({
      has: ui.getByRole("heading", { name: "review-kit", exact: true }),
    });
    await expect(plugin).toContainText("Installed · enabled");
    await expect(plugin).toContainText("Update available");
    expect(calls.filter((c) => c.name === "manage_codex_plugins")).toHaveLength(
      0,
    );
    await ui
      .getByRole("button", { name: "Check for plugin updates", exact: true })
      .click();
    await expect(
      ui.getByRole("button", { name: "Update review-kit", exact: true }),
    ).toBeEnabled();
    expect(
      calls
        .filter((c) => c.name === "manage_codex_plugins")
        .map((c) => c.arguments.action),
    ).toEqual(["check-updates"]);
    await ui
      .getByRole("button", { name: "Update review-kit", exact: true })
      .focus();
    await page.keyboard.press("Enter");
    await expect(plugin).toContainText("Installed version: 2.0.0");
    await expect(plugin).toContainText("Matches checked marketplace version");
    await ui.getByLabel("Include available plugins").check();
    await ui
      .getByRole("button", { name: "Install extra-kit", exact: true })
      .click();
    const extra = ui.locator(".plugin-row").filter({
      has: ui.getByRole("heading", { name: "extra-kit", exact: true }),
    });
    await expect(extra).toContainText("Installed · enabled");
    await ui
      .getByRole("button", { name: "Remove extra-kit", exact: true })
      .click();
    await expect(extra).toContainText("Not installed");
    await ui
      .getByLabel("Marketplace source", { exact: true })
      .fill("owner/custom-plugins");
    await ui.getByLabel("Marketplace Git ref (optional)").fill("stable");
    await ui
      .getByRole("button", { name: "Add marketplace", exact: true })
      .click();
    await expect
      .poll(
        () =>
          calls.filter((c) => c.name === "manage_codex_plugins").at(-1)
            ?.arguments,
      )
      .toMatchObject({
        action: "add-marketplace",
        target: "owner/custom-plugins",
        ref: "stable",
      });
    await expect(
      ui.getByRole("button", { name: "Discover pipeline skills", exact: true }),
    ).toBeEnabled();
    await scanOwnedApp(page, `admin-plugins-${theme}`);
    expect(
      await ui.locator("body").evaluate((e) => e.scrollWidth <= e.clientWidth),
    ).toBe(true);
    const reads = calls.filter((c) => c.name === "read_plugin_admin");
    expect(reads.length).toBeGreaterThan(2);
    expect(reads.every((c) => typeof c.arguments.freshness === "string")).toBe(true);
    expect(new Set(reads.map((c) => c.arguments.freshness)).size).toBe(reads.length);
  });
}

test("uncertain plugin mutations preserve drafts and never replay automatically", async ({
  page,
}) => {
  const { ui, calls } = await host(page, {
    surface: "settings",
    toolError: {
      name: "manage_codex_plugins",
      message: "Delivery could not be confirmed",
    },
  });
  await ui.getByText("Plugins & skills", { exact: true }).click();
  await ui
    .getByLabel("Plugin selector", { exact: true })
    .fill("my-review@my-market");
  await ui
    .getByRole("button", { name: "Install selected plugin", exact: true })
    .click();
  await expect(ui.getByRole("alert")).toContainText(
    "Delivery could not be confirmed",
  );
  await expect(ui.getByLabel("Plugin selector", { exact: true })).toHaveValue(
    "my-review@my-market",
  );
  const reads = calls.filter((c) => c.name === "read_plugin_admin").length;
  await ui.getByLabel("Search Codex plugins").fill("review");
  await expect.poll(() => calls.filter((c) => c.name === "read_plugin_admin").length).toBeGreaterThan(reads);
  await expect(ui.getByRole("alert")).toContainText("Delivery could not be confirmed");
  expect(calls.filter((c) => c.name === "manage_codex_plugins")).toHaveLength(
    1,
  );
});

test("plugin status polling clears recovered read failures while retaining drafts", async ({ page }) => {
  let unavailable = true;
  const { ui, calls } = await host(page, {
    surface: "settings",
    transportFault: (name) => unavailable && name === "read_plugin_admin",
  });
  await ui.getByText("Plugins & skills", { exact: true }).click();
  await ui.getByLabel("Plugin selector", { exact: true }).fill("retained@draft");
  const readError = ui.getByRole("alert").filter({ hasText: "Could not read plugin status" });
  await expect(readError).toBeVisible();
  unavailable = false;
  await expect(readError).toHaveCount(0, { timeout: 15000 });
  await expect(ui.getByRole("heading", { name: "review-kit", exact: true })).toBeVisible();
  await expect(ui.getByLabel("Plugin selector", { exact: true })).toHaveValue("retained@draft");
  expect(calls.filter((c) => c.name === "manage_codex_plugins")).toHaveLength(0);
});

test("system defaults display and retain a supported nonpreset plugin interval", async ({ page }) => {
  const snapshot = initial();
  snapshot.config.pluginCheckMinutes = 30;
  const { ui, calls, state } = await host(page, { snapshot, surface: "settings" });
  await ui.getByText("System defaults", { exact: true }).click();
  const interval = ui.getByLabel("Plugin update check interval");
  await expect(interval).toHaveValue("30");
  await expect(interval.locator("option:checked")).toHaveText("Every 30 minutes");
  await ui.getByLabel("Request budget per hour", { exact: true }).fill("120");
  await ui.getByRole("button", { name: "Save system defaults", exact: true }).click();
  await expect.poll(() => state.config.githubRequestsPerHour).toBe(120);
  expect(calls.find((c) => c.name === "save_settings")?.arguments.pluginCheckMinutes).toBe(30);
  expect(state.config.pluginCheckMinutes).toBe(30);
});

test("repository review pipeline overrides system defaults and plugin skills remain selectable", async ({
  page,
}) => {
  const snapshot = initial();
  snapshot.inbox[0].item.kind = "pr";
  snapshot.inbox[0].item.reviewRequests = ["alex"];
  snapshot.profiles.push(
    { ...snapshot.profiles[0], id: "system-review", name: "System reviewer" },
    { ...snapshot.profiles[0], id: "repo-review", name: "Repository reviewer" },
  );
  snapshot.config.reviewProfileId = "system-review";
  snapshot.repositories = [
    {
      id: "owner/repo",
      checkout: "",
      base: "",
      lastSync: new Date().toISOString(),
      error: "",
      reviewProfileId: "repo-review",
      autoReview: false,
    },
  ];
  snapshot.skills = [
    {
      id: "plugin-skill",
      name: "Plugin review policy",
      path: "/plugins/review/SKILL.md",
      content: "Fixture skill",
      managed: false,
    },
    {
      id: "missing-skill",
      name: "Missing plugin skill",
      path: "/missing/SKILL.md",
      content: "",
      managed: false,
      disabled: true,
    },
  ];
  const { ui, calls } = await host(page, { snapshot });
  await ui.getByRole("button", { name: "Review PRs", exact: true }).click();
  await ui.getByRole("button", { name: /P1 Fix reconnect regression/ }).click();
  await expect(ui.getByLabel("Agent pipeline", { exact: true })).toHaveValue(
    "repo-review",
  );
  expect(calls.filter((c) => c.name === "start_run")).toHaveLength(0);
  await showSettings(page);
  await ui.getByText("Launch profiles", { exact: true }).click();
  await ui
    .getByLabel("Edit profile", { exact: true })
    .selectOption("repo-review");
  await ui
    .getByRole("checkbox", { name: "Plugin review policy", exact: true })
    .check();
  await expect(
    ui.getByRole("checkbox", { name: /Missing plugin skill/ }),
  ).toBeDisabled();
  await ui.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect
    .poll(
      () =>
        calls.filter((c) => c.name === "save_profile").at(-1)?.arguments.skills,
    )
    .toEqual(["plugin-skill"]);
});

for (const theme of ["light", "dark"] as const) {
  test(`GitHub usage settings show cached quota and save budget without polling in ${theme}`, async ({page})=>{
    await page.setViewportSize({width:390,height:820});
    const snapshot=initial();snapshot.githubUsage={requestsLastHour:12,budget:600,reserve:100,nextRequest:"",restRemaining:1200,restReset:"",graphqlRemaining:2400,graphqlReset:""};
    const {ui,calls}=await host(page,{snapshot,theme,surface:"settings"});
    await ui.getByText("System defaults",{exact:true}).click();
    const usage=ui.getByRole("group",{name:"GitHub API usage"});
    await expect(usage).toContainText("12 / 600 requests");
    await expect(usage).toContainText("REST remaining: 1200");
    await usage.getByLabel("Request budget per hour",{exact:true}).fill("120");
    await usage.getByLabel("Keep GitHub requests in reserve",{exact:true}).fill("200");
    await usage.getByLabel("Review notification poll interval",{exact:true}).selectOption("300");
    await ui.getByRole("button",{name:"Save system defaults",exact:true}).click();
    await expect.poll(()=>calls.filter(c=>c.name==="save_settings").length).toBe(1);
    expect(calls.find(c=>c.name==="save_settings")!.arguments).toMatchObject({githubRequestsPerHour:120,githubReserve:200,githubPollSeconds:300});
    expect(calls.some(c=>["sync_repositories","read_item","start_run","prepare_launch"].includes(c.name))).toBe(false);
    await expect(ui.locator("body")).toHaveJSProperty("scrollWidth",await ui.locator("body").evaluate(e=>e.clientWidth));
    await scanOwnedApp(page,`github-budget-${theme}`);
  });
}

test("full resync reloads plugin administration at an earlier cursor without clearing its draft",async({page})=>{
  const snapshot=initial();snapshot.cursor=100;
  const {ui,state,calls}=await host(page,{snapshot,surface:"settings",watchDomains:["plugin-inventory"]});
  await ui.getByText("Plugins & skills",{exact:true}).click();
  await ui.getByLabel("Plugin selector",{exact:true}).fill("retained@restore");
  await expect.poll(()=>calls.filter(c=>c.name==="read_plugin_admin").length).toBe(1);
  state.cursor=101;
  await expect.poll(()=>calls.filter(c=>c.name==="read_plugin_admin").length).toBe(2);
  state.cursor=3;
  await expect.poll(()=>calls.filter(c=>c.name==="read_plugin_admin").length).toBe(3);
  await expect(ui.getByLabel("Plugin selector",{exact:true})).toHaveValue("retained@restore");
  expect(calls.filter(c=>c.name==="manage_codex_plugins")).toHaveLength(0);
});

test("the managed skill editor loads selected saved instructions and saves an existing skill",async({page})=>{

  const snapshot=initial();
  const original="---\nname: saved-skill\ndescription: Saved instructions.\n---\nKeep existing guidance.\n";
  snapshot.skills=[{id:"managed:saved-skill",name:"saved-skill",path:"/fixture/skill",content:original,managed:true}];
  const {ui,calls}=await host(page,{snapshot,surface:"settings"});
  await ui.getByText("Managed skills",{exact:true}).click();
  expect(calls.filter(c=>c.name==="read_ui_state").every(c=>!(c.arguments.views as string[]).includes("skill"))).toBe(true);
  await ui.getByLabel("Edit managed skill").selectOption("managed:saved-skill");
  const content=ui.getByLabel("Skill instructions",{exact:true});
  await expect(content).toHaveValue(original);
  const edited=original+"Additional reviewed guidance.\n";
  await content.fill(edited);
  await ui.getByRole("button",{name:"Save skill",exact:true}).click();
  await expect.poll(()=>calls.filter(c=>c.name==="save_skill").map(c=>c.arguments.content)).toEqual([edited]);
  expect(calls.filter(c=>c.name==="read_ui_state").some(c=>(c.arguments.views as string[]).includes("skill")&&c.arguments.id==="managed:saved-skill")).toBe(true);
});

test("a late managed skill read cannot overwrite a later selection or its editor draft",async({page})=>{
  const snapshot=initial();
  const first="---\nname: first\n---\nFirst saved instructions.";
  const second="---\nname: second\n---\nSecond saved instructions.";
  snapshot.skills=[{id:"managed:first",name:"first",path:"/fixture/first",content:first,managed:true},{id:"managed:second",name:"second",path:"/fixture/second",content:second,managed:true}];
  let release:()=>void=()=>{};
  const gate=new Promise<void>(resolve=>{release=resolve});
  let pending=false,returned=false;
  const {ui}=await host(page,{snapshot,surface:"settings",readUIState:async(data,args)=>{
    if((args.views as string[]).includes("skill")&&args.id==="managed:first"){pending=true;await gate;returned=true}
    return data;
  }});
  try{
    await ui.getByText("Managed skills",{exact:true}).click();
    await ui.getByLabel("Edit managed skill").selectOption("managed:first");
    await expect.poll(()=>pending).toBe(true);
    await expect(ui.getByRole("button",{name:"Save skill",exact:true})).toBeDisabled();
    await ui.getByLabel("Edit managed skill").selectOption("managed:second");
    const content=ui.getByLabel("Skill instructions",{exact:true});
    await expect(content).toHaveValue(second);
    await content.fill(second+" Keep this local draft.");
    release();
    await expect.poll(()=>returned).toBe(true);
    await content.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))));
    await expect(content).toHaveValue(second+" Keep this local draft.");
    await expect(ui.getByLabel("Skill name",{exact:true})).toHaveValue("second");
  }finally{release()}
});

test("managed skill read failure blocks saving until an explicit read retry succeeds",async({page})=>{
  const snapshot=initial();
  const original="---\nname: saved\n---\nKeep saved instructions.";
  snapshot.skills=[{id:"managed:saved",name:"saved",path:"/fixture/saved",content:original,managed:true}];
  let unavailable=true;
  const {ui,calls}=await host(page,{snapshot,surface:"settings",readUIState:async(data,args)=>{
    if((args.views as string[]).includes("skill")&&unavailable)throw new Error("Saved editor unavailable");
    return data;
  }});
  await ui.getByText("Managed skills",{exact:true}).click();
  await ui.getByLabel("Edit managed skill").selectOption("managed:saved");
  await expect(ui.getByRole("alert")).toContainText("Saved editor unavailable");
  await expect(ui.getByRole("button",{name:"Save skill",exact:true})).toBeDisabled();
  expect(calls.filter(c=>c.name==="save_skill")).toHaveLength(0);
  unavailable=false;
  await ui.getByRole("button",{name:"Reload selected skill",exact:true}).click();
  await expect(ui.getByLabel("Skill instructions",{exact:true})).toHaveValue(original);
  await expect(ui.getByRole("button",{name:"Save skill",exact:true})).toBeEnabled();
  await expect(ui.getByRole("alert")).toHaveCount(0);
  expect(calls.filter(c=>c.name==="read_ui_state"&&(c.arguments.views as string[]).includes("skill"))).toHaveLength(2);
  expect(calls.filter(c=>c.name==="save_skill")).toHaveLength(0);
});

test("notification-only failures refresh saved Settings feedback",async({page})=>{
  const snapshot=initial();snapshot.githubUsage={requestsLastHour:0,budget:600,reserve:100,nextRequest:"",restReset:"",graphqlReset:""};
  const {ui,state,calls}=await host(page,{snapshot,surface:"settings",watchDomains:["github-notifications"]});
  await ui.getByText("System defaults",{exact:true}).click();
  state.githubUsage!.notificationError="Saved credential resolution failed";state.cursor++;
  await expect(ui.getByRole("alert")).toContainText("Saved credential resolution failed");
  expect(calls.filter(c=>["sync_repositories","diagnostics"].includes(c.name))).toHaveLength(0);
});

test("absent global reviewers retain invalid draft and validity through unrelated settings edits", async ({page})=>{
 const {ui,calls,state}=await host(page,{surface:"settings"});
 expect(state.config.reviewers).toBeUndefined();
 await ui.getByText("System defaults",{exact:true}).click();
 const defaults=ui.locator("details").filter({has:ui.locator("summary").filter({hasText:/^System defaults$/})});
 const configuration=defaults.getByRole("group",{name:"Review workflow configuration"});
 const reviewers=configuration.getByLabel("Ordered reviewers");
 await reviewers.fill("{invalid");
 const error=await configuration.getByRole("alert").innerText();
 expect(await reviewers.evaluate(e=>(e as HTMLTextAreaElement).validity.customError)).toBe(true);
 await defaults.getByLabel("Request budget per hour",{exact:true}).fill("1400");
 await expect(reviewers).toHaveValue("{invalid");
 await expect(configuration.getByRole("alert")).toHaveText(error);
 expect(await reviewers.evaluate(e=>(e as HTMLTextAreaElement).validity.customError)).toBe(true);
 await defaults.getByLabel("Automatically post completed reviews",{exact:true}).check();
 await expect(reviewers).toHaveValue("{invalid");
 await expect(configuration.getByRole("alert")).toHaveText(error);
 expect(await reviewers.evaluate(e=>(e as HTMLTextAreaElement).validity.customError)).toBe(true);
 await defaults.getByRole("button",{name:"Save system defaults",exact:true}).click();
 expect(calls.filter(c=>c.name==="save_settings")).toHaveLength(0);
 await expect(reviewers).toHaveValue("{invalid");
 await expect(configuration.getByRole("alert")).toHaveText(error);
 expect(await reviewers.evaluate(e=>(e as HTMLTextAreaElement).validity.valid)).toBe(false);
 expect(state.config.reviewers).toBeUndefined();
 expect(calls.filter(c=>["start_run","post_retained_review"].includes(c.name))).toHaveLength(0);
});

test("review workflow settings preserve ordered bindings and inherited posting overrides", async ({page})=>{
 await page.setViewportSize({width:320,height:820});
 const snapshot=initial();const {ui,calls,state}=await host(page,{snapshot,surface:"settings"});
 await ui.getByText("System defaults",{exact:true}).click();
 await expect(ui.locator("body")).toHaveJSProperty("scrollWidth",await ui.locator("body").evaluate(e=>e.clientWidth));
 await expect(ui.getByLabel("Automatically post completed reviews")).not.toBeChecked();
 await ui.getByLabel("Automatically post completed reviews").check();
 const config=ui.getByRole("group",{name:"Review workflow configuration"}).last();
 const reviewers=[{id:"first",kind:"skill",skillId:"fixture"},{id:"second",kind:"command",argv:["/local/reviewer","{handoff}"]}];
 await config.getByLabel("Ordered reviewers").fill(JSON.stringify(reviewers));
 await config.getByLabel("Private review rules").fill("Use inline comment reviews.");
 await ui.getByRole("button",{name:"Save system defaults",exact:true}).click();
 await expect.poll(()=>state.config.reviewers).toEqual(reviewers);
 expect(calls.find(c=>c.name==="save_settings")?.arguments.autoPostReview).toBe(true);
 await ui.getByLabel("GitHub repository",{exact:true}).fill("owner/repo");
 await ui.getByLabel("Repository automatic review posting").selectOption("false");
 await ui.getByRole("button",{name:"Save repository",exact:true}).click();
 await expect.poll(()=>state.repositories[0]?.autoPostReview).toBe(false);
 await ui.getByRole("button",{name:"Edit repository owner/repo",exact:true}).click();
 await ui.getByLabel("Repository automatic review posting").selectOption("inherit");
 const repositoryConfig=ui.getByRole("group",{name:"Review workflow configuration"}).first();
 await repositoryConfig.getByLabel("Inherit system reviewers",{exact:true}).uncheck();
 const repositoryReviewers=repositoryConfig.getByLabel("Ordered reviewers");
 await repositoryReviewers.fill("{invalid");
 await expect(repositoryConfig.getByRole("alert")).toBeVisible();
 expect(await repositoryReviewers.evaluate(e=>(e as HTMLTextAreaElement).validity.customError)).toBe(true);
 const savedRepositories=calls.filter(c=>c.name==="save_repository").length;
 await ui.getByRole("button",{name:"Save repository",exact:true}).click();
 expect(calls.filter(c=>c.name==="save_repository")).toHaveLength(savedRepositories);
 await expect(repositoryReviewers).toHaveValue("{invalid");
 await repositoryConfig.getByLabel("Inherit system reviewers",{exact:true}).check();
 await expect(repositoryReviewers).toBeDisabled();
 await repositoryConfig.getByLabel("Inherit system reviewers",{exact:true}).uncheck();
 await expect(repositoryReviewers).toBeEnabled();
 await expect(repositoryReviewers).toHaveValue("[]");
 await expect(repositoryConfig.getByRole("alert")).toHaveCount(0);
 expect(await repositoryReviewers.evaluate(e=>(e as HTMLTextAreaElement).validity.valid)).toBe(true);
 await ui.getByRole("button",{name:"Save repository",exact:true}).click();
 await expect.poll(()=>calls.filter(c=>c.name==="save_repository").length).toBe(savedRepositories+1);
 await expect.poll(()=>state.repositories[0]?.autoPostReview).toBeNull();
 expect(state.repositories[0]?.reviewers).toEqual([]);
 await expect(ui.locator("body")).toHaveJSProperty("scrollWidth",await ui.locator("body").evaluate(e=>e.clientWidth));
 expect(calls.filter(c=>["start_run","post_retained_review"].includes(c.name))).toHaveLength(0);
});

test("global DK review import preserves unrelated edits made while settings are loading", async ({page})=>{
 const reviewers=[{id:"imported",kind:"skill" as const,skillId:"fixture"}];
 let release!:()=>void;
 let loading=false;
 const gate=new Promise<void>(resolve=>{release=resolve});
 const {ui,calls,state}=await host(page,{
  surface:"settings",
  dkReviewSettings:{reviewers,reviewRules:"Imported private rules."},
  readSettings:async config=>{loading=true;await gate;return config},
 });
 await ui.getByText("System defaults",{exact:true}).click();
 const defaults=ui.locator("details").filter({has:ui.locator("summary").filter({hasText:/^System defaults$/})});
 await defaults.getByLabel("Ordered reviewers").fill("{invalid");
 await expect(defaults.getByRole("alert")).toBeVisible();
 expect(await defaults.getByLabel("Ordered reviewers").evaluate(e=>(e as HTMLTextAreaElement).validity.customError)).toBe(true);
 try{
  await defaults.getByRole("button",{name:"Import DK review settings once",exact:true}).click();
  await expect.poll(()=>loading).toBe(true);
  await defaults.getByLabel("Request budget per hour",{exact:true}).fill("1400");
  await defaults.getByLabel("Plugin update check interval").selectOption("60");
  await defaults.getByLabel("Automatically post completed reviews",{exact:true}).check();
  release();
  await expect(defaults.getByRole("status").filter({hasText:/Imported: reviewers, reviewRules/})).toBeVisible();
  await expect(defaults.getByLabel("Ordered reviewers")).toHaveValue(JSON.stringify(reviewers,null,2));
  await expect(defaults.getByRole("alert")).toHaveCount(0);
  expect(await defaults.getByLabel("Ordered reviewers").evaluate(e=>(e as HTMLTextAreaElement).validity.valid)).toBe(true);
  await expect(defaults.getByLabel("Private review rules")).toHaveValue("Imported private rules.");
  await expect(defaults.getByLabel("Request budget per hour",{exact:true})).toHaveValue("1400");
  await expect(defaults.getByLabel("Plugin update check interval")).toHaveValue("60");
  await expect(defaults.getByLabel("Automatically post completed reviews",{exact:true})).toBeChecked();
  await defaults.getByRole("button",{name:"Save system defaults",exact:true}).click();
  await expect.poll(()=>calls.filter(c=>c.name==="save_settings").length).toBe(1);
  expect(state.config).toMatchObject({reviewers,reviewRules:"Imported private rules.",githubRequestsPerHour:1400,pluginCheckMinutes:60,autoPostReview:true});
  expect(calls.filter(c=>["start_run","post_retained_review"].includes(c.name))).toHaveLength(0);
 }finally{release()}
});

test("single Codex execution settings retain the selected iteration skill without launching", async ({page}) => {
  const snapshot=initial();
  snapshot.skills.push({id:"iteration",name:"dk-workflows:dk-pr-iter",path:"/fixture/SKILL.md",content:"",managed:false});
  const {ui,calls,state}=await host(page,{snapshot,surface:"settings"});
  await ui.getByText("System defaults",{exact:true}).click();
  await expect(ui.getByLabel("Session execution",{exact:true})).toHaveValue("workflow");
  await ui.getByLabel("Session execution",{exact:true}).selectOption("single-codex");
  await ui.getByLabel("PR iteration skill",{exact:true}).selectOption("iteration");
  await ui.getByRole("button",{name:"Save system defaults",exact:true}).click();
  await expect.poll(()=>state.config.executionMode).toBe("single-codex");
  expect(state.config.prReviewSkillId).toBe("iteration");
  await ui.getByLabel("Request budget per hour",{exact:true}).fill("1400");
  await ui.getByRole("button",{name:"Save system defaults",exact:true}).click();
  await expect.poll(()=>state.config.githubRequestsPerHour).toBe(1400);
  expect(state.config.executionMode).toBe("single-codex");
  expect(state.config.prReviewSkillId).toBe("iteration");
  await ui.getByLabel("Session execution",{exact:true}).selectOption("workflow");
  await expect(ui.getByLabel("PR iteration skill",{exact:true})).toHaveCount(0);
  await ui.getByRole("button",{name:"Save system defaults",exact:true}).click();
  await expect.poll(()=>state.config.executionMode).toBe("workflow");
  expect(calls.filter(c=>["start_run","prepare_pr_review","post_retained_review"].includes(c.name))).toHaveLength(0);
});
