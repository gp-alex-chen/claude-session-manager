// A Storage-compatible cache backed by application JSON, shared by controllers.
export const PREFERENCE_KEYS = [
  'ui-theme', 'term-theme', 'term-font-size', 'terminal-layout-mode',
  'terminal-layout-split-ratios', 'update-last-check-date',
  'session-provider-filter', 'quota-source',
  'sidebar-width', 'sidebar-collapsed',
];

export function createPreferenceStorage({ backend, onError }) {
  const values = new Map();
  const supported = new Set(PREFERENCE_KEYS);
  let initialized = false;
  let writable = false;
  let loading = null;
  let pending = Promise.resolve();
  const persisted = new Map();

  function queue(key, value) {
    pending = pending.then(async () => {
      try {
        await backend.SetPreference(key, value);
        persisted.set(key, value);
      } catch (error) {
        onError?.(error);
      }
    });
  }

  function initialize() {
    if (loading) return loading;
    loading = (async () => {
      try {
        const saved = await backend.LoadPreferences();
        if (!saved || typeof saved !== 'object' || Array.isArray(saved)) {
          throw new Error('无法读取应用配置');
        }
        for (const key of PREFERENCE_KEYS) {
          if (typeof saved[key] === 'string') {
            values.set(key, saved[key]);
            persisted.set(key, saved[key]);
          }
        }
        writable = true;
      } catch (error) {
        onError?.(error);
      } finally {
        initialized = true;
      }
    })();
    return loading;
  }

  return {
    initialize,
    getItem: (key) => values.get(key) ?? null,
    setItem(key, value) {
      if (!supported.has(key) || !initialized || !writable) return;
      value = String(value);
      if (values.get(key) === value && persisted.get(key) === value) return;
      values.set(key, value);
      queue(key, value);
    },
    flush: () => pending,
  };
}
