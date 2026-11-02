//go:build !windows && !darwin && !linux

package desktop

import "aether-cockpit/internal/core/gateway"

func preparePlatform() {}

func platformCapabilities() *gateway.PlatformCapabilities { return nil }
