//go:build !linux

package main

func acquireLabLease() (func(), error) {
	return func() {}, nil
}
