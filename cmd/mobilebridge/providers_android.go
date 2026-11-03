//go:build android

package main

import (
	// Register the full desktop channel matrix in the Android runtime.
	_ "aether-cockpit/internal/mobileproviders"
)
