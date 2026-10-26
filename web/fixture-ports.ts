const configured = process.env.CODE_FACTORY_TEST_BASE_PORT || "4173";
const basePort = Number(configured);
if (/[^0-9]/.test(configured) || basePort < 1 || basePort > 65534)
  throw new Error(
    "CODE_FACTORY_TEST_BASE_PORT must be a decimal integer from 1 to 65534",
  );

export const fixtureUIPort = basePort;
export const fixtureUIOrigin = `http://127.0.0.1:${basePort}`;
export const fixtureDaemonOrigin = `http://127.0.0.1:${basePort + 1}`;
