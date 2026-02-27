#!/usr/bin/env node

const MODULE_PATH =
  process.env.FORTIVPN_MODULE_PATH ||
  '/Applications/FortiClient.app/Contents/Resources/app.asar.unpacked/assets/js/guimessenger_jyp.node';

function parsePayload(raw) {
  if (!raw) {
    return {};
  }
  return JSON.parse(raw);
}

async function normalize(value) {
  const resolved = value && typeof value.then === 'function' ? await value : value;

  if (typeof resolved !== 'string') {
    return resolved;
  }
  if (resolved.trim() === '') {
    return '';
  }

  try {
    return JSON.parse(resolved);
  } catch {
    return resolved;
  }
}

async function main() {
  const action = process.argv[2];
  if (!action) {
    throw new Error('missing action');
  }

  let api;
  try {
    api = require(MODULE_PATH);
  } catch (err) {
    throw new Error(`failed to load FortiClient module: ${err.message}`);
  }

  const payload = parsePayload(process.argv[3]);

  switch (action) {
    case 'list-connections': {
      return normalize(api.GetVPNConnectionList());
    }
    case 'get-state': {
      return normalize(api.getConnectionState());
    }
    case 'connect': {
      const request = {
        connection_name: payload.connection_name || '',
        connection_type: payload.connection_type || 'ssl',
      };
      return normalize(api.ConnectTunnel(JSON.stringify(request)));
    }
    case 'disconnect': {
      const request = {
        connection_name: payload.connection_name || '',
        connection_type: payload.connection_type || 'ssl',
      };
      return normalize(api.DisconnectTunnel(JSON.stringify(request)));
    }
    default:
      throw new Error(`unknown action: ${action}`);
  }
}

(async () => {
  try {
    const result = await main();
    process.stdout.write(JSON.stringify({ ok: true, result }));
  } catch (err) {
    const message = err && err.message ? err.message : String(err);
    process.stdout.write(JSON.stringify({ ok: false, error: message }));
    process.exitCode = 1;
  }
})();
