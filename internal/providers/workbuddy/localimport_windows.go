//go:build windows

package workbuddy

// WorkBuddy's local login state lives in the Windows desktop client's
// %LOCALAPPDATA% credential files.
const localImportSupported = true
