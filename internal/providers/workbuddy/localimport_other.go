//go:build !windows

package workbuddy

// The desktop client's credential directory and its DPAPI-bound key are
// Windows-only; other hosts must use the manual token / user_id flow.
const localImportSupported = false
