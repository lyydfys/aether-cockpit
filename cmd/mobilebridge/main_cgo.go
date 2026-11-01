//go:build cgo

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unsafe"

	"aether-cockpit/internal/mobile"
	"aether-cockpit/internal/mobilebridge"
)

//export nativeBridgeVersion
func nativeBridgeVersion(buffer *C.char, capacity C.int) C.int {
	if buffer == nil || capacity < 0 {
		return C.int(-len(mobilebridge.ABIVersion) - 1)
	}
	return C.int(mobilebridge.WriteABIVersion(unsafe.Slice((*byte)(unsafe.Pointer(buffer)), int(capacity))))
}

var bridge struct {
	mu sync.Mutex
	rt *mobile.Runtime
}

//export nativeStart
func nativeStart(configJSON *C.char, out *C.char, capacity C.int) C.int {
	if configJSON == nil {
		return writeResult(out, capacity, encodeStatus(mobile.RuntimeStatus{
			State:     mobile.StateFailed,
			ErrorCode: "invalid_config",
		}))
	}
	return writeResult(out, capacity, start(C.GoString(configJSON)))
}

//export nativeGetStatus
func nativeGetStatus(out *C.char, capacity C.int) C.int {
	return writeResult(out, capacity, currentStatus())
}

//export nativeStop
func nativeStop(out *C.char, capacity C.int) C.int {
	return writeResult(out, capacity, stop())
}

func start(configJSON string) string {
	var cfg mobile.RuntimeConfig
	decoder := json.NewDecoder(strings.NewReader(configJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return encodeStatus(mobile.RuntimeStatus{State: mobile.StateFailed, ErrorCode: "invalid_config"})
	}

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.rt != nil {
		status := bridge.rt.Status()
		if status.State == mobile.StateRunning ||
			status.State == mobile.StateStarting ||
			status.State == mobile.StateStopping {
			return encodeStatus(status)
		}
	}
	rt, err := mobile.Start(context.Background(), cfg)
	if err != nil {
		code := "start_failed"
		var startErr *mobile.StartError
		if errors.As(err, &startErr) {
			code = startErr.Code
		}
		return encodeStatus(mobile.RuntimeStatus{State: mobile.StateFailed, ErrorCode: code})
	}
	bridge.rt = rt
	return encodeStatus(rt.Status())
}

func currentStatus() string {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.rt == nil {
		return encodeStatus(mobile.RuntimeStatus{State: mobile.StateStopped})
	}
	return encodeStatus(bridge.rt.Status())
}

func stop() string {
	bridge.mu.Lock()
	rt := bridge.rt
	bridge.mu.Unlock()
	if rt == nil {
		return encodeStatus(mobile.RuntimeStatus{State: mobile.StateStopped})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := rt.Stop(ctx)
	status := rt.Status()
	if err != nil {
		status.ErrorCode = "stop_timeout"
	}
	return encodeStatus(status)
}

func encodeStatus(status mobile.RuntimeStatus) string {
	data, err := json.Marshal(status)
	if err != nil {
		return `{"state":"failed","errorCode":"encode_failed"}`
	}
	return string(data)
}

func writeResult(out *C.char, capacity C.int, value string) C.int {
	if out == nil || capacity < 0 {
		return C.int(-len(value) - 1)
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(out)), int(capacity))
	needed := len(value) + 1
	if len(buf) < needed {
		return C.int(-needed)
	}
	copy(buf, value)
	buf[len(value)] = 0
	return C.int(needed)
}

func main() {}
