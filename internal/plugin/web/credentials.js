const CONFIGURED_CREDENTIAL_FIELDS = [
  ["gemini-api-key", "gemini", "gemini:apikey", true],
  ["interactions-api-key", "gemini-interactions", "gemini-interactions:apikey", true],
  ["claude-api-key", "claude", "claude:apikey", true],
  ["codex-api-key", "codex", "codex:apikey", true],
  ["xai-api-key", "xai", "xai:apikey", true],
  ["meta-api-key", "meta", "meta:apikey", true],
  ["vertex-api-key", "vertex", "vertex:apikey", false]
];

function sortedConfiguredHeaders(headers) {
  if (!headers) return "";
  return Object.keys(headers).sort().map((key) => key + "\0" + headers[key] + "\0").join("");
}

async function sha256Hex(value) {
  const bytes = new TextEncoder().encode(value);
  if (window.crypto?.subtle) {
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
  }
  // Public HTTP origins lack SubtleCrypto; identity hashes must still match.
  const constants = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be,
    0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa,
    0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85,
    0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
    0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f,
    0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
  ];
  const hash = new Uint32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
  const padded = new Uint8Array(Math.ceil((bytes.length + 9) / 64) * 64);
  padded.set(bytes);
  padded[bytes.length] = 0x80;
  const data = new DataView(padded.buffer);
  const bitLength = bytes.length * 8;
  data.setUint32(padded.length - 8, Math.floor(bitLength / 0x100000000));
  data.setUint32(padded.length - 4, bitLength >>> 0);
  const words = new Uint32Array(64);
  const rotate = (word, bits) => (word >>> bits) | (word << (32 - bits));
  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let i = 0; i < 16; i++) words[i] = data.getUint32(offset + i * 4);
    for (let i = 16; i < 64; i++) {
      const x = words[i - 15], y = words[i - 2];
      const s0 = rotate(x, 7) ^ rotate(x, 18) ^ (x >>> 3);
      const s1 = rotate(y, 17) ^ rotate(y, 19) ^ (y >>> 10);
      words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }
    let [a, b, c, d, e, f, g, h] = hash;
    for (let i = 0; i < 64; i++) {
      const sum1 = rotate(e, 6) ^ rotate(e, 11) ^ rotate(e, 25);
      const choice = (e & f) ^ (~e & g);
      const t1 = (h + sum1 + choice + constants[i] + words[i]) >>> 0;
      const sum0 = rotate(a, 2) ^ rotate(a, 13) ^ rotate(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (sum0 + majority) >>> 0;
      h = g;
      g = f;
      f = e;
      e = (d + t1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (t1 + t2) >>> 0;
    }
    const block = [a, b, c, d, e, f, g, h];
    for (let i = 0; i < 8; i++) hash[i] += block[i];
  }
  return Array.from(hash, (word) => word.toString(16).padStart(8, "0")).join("");
}

async function configuredCredentialRef(kind, parts, counters) {
  // These hashes must stay byte-identical to CPA's StableIDGenerator and
  // the plugin's CredentialFingerprint so exact bindings match candidates.
  const digest = await sha256Hex(kind + parts.map((part) => "\0" + String(part || "").trim()).join(""));
  const base = kind + ":" + digest.slice(0, 12);
  const collision = counters.get(base) || 0;
  counters.set(base, collision + 1);
  const id = collision ? base + "-" + collision : base;
  return "sha256:" + (await sha256Hex("cpa-key-billing:credential:v1\0" + id));
}

function parseConfiguredAPIKeys(config) {
  const keys = config?.["api-keys"];
  if (keys === null) return [];
  if (!Array.isArray(keys) || keys.some((key) => typeof key !== "string")) throw new UIError(m("ui.invalid_cpa_api_key_list"));
  return keys;
}

function configuredCredentialEntries(config, field) {
  const entries = config[field] == null ? [] : requireArray(config[field], field);
  const validateEntry = (entry) => {
    requireObject(entry, field);
    for (const key of ["api-key", "base-url", "proxy-url", "prefix", "name"])
      if (entry[key] != null && typeof entry[key] !== "string") throw new UIError(m("ui.invalid_configuration_for_value", { v0: field }));
    if (entry.disabled != null && typeof entry.disabled !== "boolean")
      throw new UIError(m("ui.invalid_enabled_state_for_value", { v0: field }));
    if (entry.weight != null && (typeof entry.weight !== "number" || !Number.isFinite(entry.weight)))
      throw new UIError(m("ui.invalid_weight_for_value", { v0: field }));
    if (
      entry["excluded-models"] != null &&
      (!Array.isArray(entry["excluded-models"]) || entry["excluded-models"].some((model) => typeof model !== "string"))
    )
      throw new UIError(m("ui.invalid_configuration_for_value", { v0: field }));
    if (entry.headers != null) {
      requireObject(entry.headers, field);
      if (Object.values(entry.headers).some((value) => typeof value !== "string"))
        throw new UIError(m("ui.invalid_header_configuration_for_value", { v0: field }));
    }
  };
  for (const entry of entries) {
    validateEntry(entry);
    if (field === "openai-compatibility" && entry["api-key-entries"] != null) {
      for (const key of requireArray(entry["api-key-entries"], field)) validateEntry(key);
    }
  }
  return entries;
}

async function parseConfiguredCredentials(config) {
  const generation = sessionGeneration;
  const counters = new Map();
  const credentials = [];

  for (const [field, provider, kind, usesHeaders] of CONFIGURED_CREDENTIAL_FIELDS) {
    for (const entry of configuredCredentialEntries(config, field)) {
      const apiKey = (entry["api-key"] || "").trim();
      const baseURL = (entry["base-url"] || "").trim();
      if (!apiKey && !baseURL) continue;
      const parts = [apiKey, baseURL, entry["proxy-url"]];
      if (usesHeaders) parts.push(entry.prefix, sortedConfiguredHeaders(entry.headers));
      credentials.push({
        ref: await configuredCredentialRef(kind, parts, counters),
        provider,
        display_name: previewCredential(apiKey),
        disabled: (entry["excluded-models"] || []).some((model) => model.trim() === "*")
      });
    }
  }

  for (const entry of configuredCredentialEntries(config, "openai-compatibility")) {
    // CPA does not create runtime credentials or consume IDs for disabled providers.
    if (entry.disabled === true) continue;
    const providerName = (entry.name || "").trim().toLowerCase() || "openai-compatibility";
    const provider =
      providerName === "openai-compatibility" || providerName.startsWith("openai-compatible-")
        ? providerName
        : "openai-compatible-" + providerName;
    const apiKeys = entry["api-key-entries"] || [];
    if (!apiKeys.length) {
      credentials.push({
        ref: await configuredCredentialRef("openai-compatibility:" + providerName, [entry["base-url"]], counters),
        provider,
        display_name: "",
        disabled: false
      });
      continue;
    }
    for (const apiKeyEntry of apiKeys) {
      const apiKey = (apiKeyEntry["api-key"] || "").trim();
      credentials.push({
        ref: await configuredCredentialRef(
          "openai-compatibility:" + providerName,
          [apiKey, entry["base-url"], apiKeyEntry["proxy-url"]],
          counters
        ),
        provider,
        display_name: previewCredential(apiKey),
        disabled: false
      });
    }
  }

  if (generation !== sessionGeneration) throw new StaleRequestError();
  return credentials;
}

function credentialName(item) {
  return window.billingI18n.serverMessage(
    item.display_name_message,
    item.display_name || (item.source === "auth-files" ? m("ui.no_email_provided") : m("ui.no_api_key_configured"))
  );
}

function credentialLabel(item) { return m("ui.join", { v0: item.provider, v1: DISPLAY_SEPARATOR, v2: credentialName(item) }); }

function credentialSourceLabel(source) { return source === "auth-files" ? m("ui.auth_files") : m("ui.ai_providers"); }

function credentialProviderLabel(item) {
  return m("ui.join", { v0: credentialSourceLabel(item.source), v1: DISPLAY_SEPARATOR, v2: item.provider });
}

function credentialIcon(item) { return item.source === "auth-files" ? "file-key" : "key"; }

function credentialSourceRank(item) { return item.source === "auth-files" ? 0 : 1; }

function compareCredentialProviders(a, b) {
  return credentialSourceRank(a) - credentialSourceRank(b) || a.provider.localeCompare(b.provider);
}

function compareCredentials(a, b) {
  return (
    credentialSourceRank(a) - credentialSourceRank(b) || String(credentialLabel(a)).localeCompare(String(credentialLabel(b)), locale())
  );
}

function sameCredentialProvider(a, b) { return a.source === b.source && a.provider === b.provider; }
