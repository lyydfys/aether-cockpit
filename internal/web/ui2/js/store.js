/* 极小状态容器：set 合并、订阅、可选持久化。 */
export function createStore(initial) {
  let state = { ...initial };
  const subs = new Set();
  return {
    get: () => state,
    set(patch) {
      const next = typeof patch === 'function' ? patch(state) : { ...state, ...patch };
      state = next;
      for (const fn of subs) { try { fn(state); } catch (e) { console.error(e); } }
      return state;
    },
    subscribe(fn) { subs.add(fn); return () => subs.delete(fn); },
  };
}
