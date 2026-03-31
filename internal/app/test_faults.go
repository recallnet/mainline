package app

import (
	"sync"
)

type testFaultHooks struct {
	before func(point string) error
}

var (
	testFaultHooksMu sync.RWMutex
	appTestHooks     testFaultHooks
)

func setAppTestFaultHooks(hooks testFaultHooks) func() {
	testFaultHooksMu.Lock()
	previous := appTestHooks
	appTestHooks = hooks
	testFaultHooksMu.Unlock()

	return func() {
		testFaultHooksMu.Lock()
		appTestHooks = previous
		testFaultHooksMu.Unlock()
	}
}

func applyAppTestFault(point string) error {
	testFaultHooksMu.RLock()
	hooks := appTestHooks
	testFaultHooksMu.RUnlock()
	if hooks.before == nil {
		return nil
	}
	return hooks.before(point)
}
