//go:build !cgo

package main

// The c-shared ABI is only built with cgo enabled, i.e. by the Android build.
func main() {}
