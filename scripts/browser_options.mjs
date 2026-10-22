// Pure controller options: selecting an endpoint never starts or inspects Chrome.
export const PILOT_BROWSER_URL = 'http://127.0.0.1:9224';

const safetyFlags = ['--no-usage-statistics', '--no-performance-crux', '--redact-network-headers'];

export function validateBrowserUrl(value) {
  // Check the spelling before URL normalization, which otherwise accepts IPv4
  // aliases such as 127.1, decimal integers and percent-encoded host names.
  if (typeof value !== 'string' || !/^http:\/\/(?:127\.0\.0\.1|\[::1\])(?::[0-9]+)?\/?$/.test(value)) {
    throw new Error('Browser URL must be an HTTP loopback literal with no credentials, path, query or fragment.');
  }
  try {
    const url = new URL(value);
    if (url.port === '0') throw new Error('Invalid endpoint port');
    return url.origin;
  } catch {
    // Never echo a rejected value: it may have contained a credential or token.
    throw new Error('Browser URL must use a valid loopback endpoint port.');
  }
}

export function parseBrowserOptions(argv = []) {
  if (!Array.isArray(argv) || argv.some(value => typeof value !== 'string')) {
    throw new Error('Browser controller options must be text arguments.');
  }
  let pilot = false;
  let browserUrl;
  for (let index = 0; index < argv.length; index++) {
    const arg = argv[index];
    if (arg === '--pilot') {
      if (pilot) throw new Error('The pilot option may be specified only once.');
      pilot = true;
    } else if (arg === '--browser-url' || arg.startsWith('--browser-url=')) {
      if (browserUrl !== undefined) throw new Error('The browser URL may be specified only once.');
      const value = arg === '--browser-url' ? argv[++index] : arg.slice('--browser-url='.length);
      browserUrl = validateBrowserUrl(value);
    } else {
      throw new Error('Unknown browser controller option.');
    }
  }
  if (pilot && browserUrl !== undefined) throw new Error('Choose either pilot mode or an explicit browser URL.');
  const mode = pilot ? 'pilot' : browserUrl !== undefined ? 'browser-url' : 'auto';
  const endpoint = pilot ? PILOT_BROWSER_URL : browserUrl;
  return { mode, browserUrl: endpoint,
    transportArgs: [...(endpoint ? ['--browserUrl', endpoint] : ['--autoConnect']), ...safetyFlags] };
}
