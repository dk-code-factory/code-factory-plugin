# Browser and installed-host testing

## Native wrapper verification

The UI has Working, Issues, My PRs, Review PRs, Messages, Configuration and Events.
Opening an item never launches work. Authorized event rules and explicit messages
start or steer one retained native conversation. Native questions/approvals keep
exact identities; uncertain delivery is reconciled without blind replay.
Configuration chat requires explicit diff confirmation for every edit. This build
uses fresh application state; earlier pilot observations do not establish its
installed-host acceptance.

This is a generic operator guide. Supply your own approved private pilot repository,
Tailscale hostname, registered app ID and installed plugin ID; none are bundled.
Read [operations](operations.md) and [acceptance](acceptance.md) first.

The service runs as the current Linux user. Verify the stable launcher and installed
binary, check for unrelated live runs, and preserve authentication, user work,
shared Tailscale routes and the existing login/lingering policy before changing
services. Scope network changes to Code Factory paths. Never reset all Serve or
Funnel configuration to repair this app.

The UI uses MCP Apps exclusively. Never embed owner credentials or add iframe
requests directly to the local daemon. Default browser control is
`node scripts/browser_control.mjs`; it attaches only to an owner-approved Chrome
session and may require native Allow. With separate explicit approval, a persistent
non-default pilot profile can use a loopback-only debugger and
`node scripts/browser_control.mjs --pilot` (port9224). The owner signs in manually;
pause inspection until confirmation and never copy authentication between profiles.
Do not expose debugging ports on LAN, Tailscale or public hosting.

The controller accepts JSON lines and prints allowed schemas. `ready:true` is not
browser attachment: discover current page IDs and obtain fresh element UIDs.
Do not reuse IDs across controller sessions or document reloads. Mask URL queries
and fragments before returning output, and inspect only the relevant app/dialog.
Do not dump credentials, cookies, headers, profiles, runtime.json, owner.token,
gateway-auth.json or whole subscription rows. Keep temporary snapshots private.

For a nested app iframe, use a current element UID in evaluate_script args and read
its ownerDocument. For controlled React text inputs, native focus/select and
ownerDocument.execCommand('insertText',false,text) may be needed; confirm the
actual enabled state and resulting server/UI state. A successful tool click alone
is not a passed assertion. Preserve selected plugin mentions in Work composers.

Open the installed complete plugin in a fresh Work chat and invoke open_factory
without launching work. Confirm the actual iframe is Connected, displays only
approved pilot data and advances sync times. Select an owner-approved native read-only profile before the first message. Ask a real provider to present Continue/Stop with request_user_input,
answer in the widget, observe its actual response and wait for idle before sending
a follow-up. Exercise reload/restart recovery and reject stale answers; resume
explicitly with retained history and workspace. Cleanup must refuse dirty work.

Verify that the actual rendered UI contains the current content-addressed resource
revision; refresh app tools and open a fresh view after upgrades. Local resource
reads or a healthy daemon cannot prove that the host discarded cached HTML.
Observe CSP enforcement directly; a development UI warning alone does not prove
whether a declared policy is active. Coordinate account-wide settings and restore
any temporary changes. Keep all evidence nonsecret and version-bound.

A connected soak must measure real authenticated app polling for at least one
hour. Local health and fixtures do not substitute for it. Coordinate desktop
logout/login with the owner because it closes desktop apps, Chrome and RDP; inspect
again only after the owner confirms authentication is complete. Preserve SSH and
existing service/lingering policy. A session cycle is not cold-boot proof.

## Native input verification

Exercise explicit working/done/blocked status reports and the suspended blocked
status response. Test exact native choices, free text, approvals and disconnect
expiry in the approved private pilot. A turn that stops without a report must
appear idle. No classifier or repair turn should run. Configure rules through a
reviewed diff and confirm quiet-period resets, human-triggered flushing and retained
history without authorizing changes to production repositories.
