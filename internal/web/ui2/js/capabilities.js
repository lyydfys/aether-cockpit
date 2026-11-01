export function accountRefreshMode(capabilities, hasCustomQuota = false) {
  if (capabilities?.quota || hasCustomQuota) return 'quota';
  if (capabilities?.refresh) return 'credentials';
  return null;
}
