const configured = process.env.CODE_FACTORY_TEST_BASE_PORT || "4173";
const basePort = Number(configured);
if (/[^0-9]/.test(configured) || basePort < 1 || basePort > 65534)
  throw new Error(
    "CODE_FACTORY_TEST_BASE_PORT must be a decimal integer from 1 to 65534",
  );

export const fixtureUIPort = basePort;
function origin(value: string | undefined, fallback: number) {
  if (value === undefined) return `http://127.0.0.1:${fallback}`;
  if (!/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(value) || +value.split(":").at(-1)! > 65535)
    throw new Error("Fixture origins must use strict IPv4 loopback and a valid port");
  return value;
}
export const fixtureUIOrigin = origin(process.env.CODE_FACTORY_TEST_UI_ORIGIN, basePort);
export const fixtureDaemonOrigin = origin(process.env.CODE_FACTORY_TEST_DAEMON_ORIGIN, basePort + 1);
