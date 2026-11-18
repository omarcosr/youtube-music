//go:build !windows

package main

// Single instance is Windows-only for now: elsewhere every launch runs.
type singleInstance struct{}

func (s *singleInstance) release() {}

func requestSingleInstance(id string, onSecond func()) *singleInstance { return &singleInstance{} }
