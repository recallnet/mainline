package state

import "sync"

type testFaultHooks struct {
	before func(point string) error
}

var (
	testFaultHooksMu sync.RWMutex
	storeTestHooks   testFaultHooks
)

func SetTestFaultHooks(before func(point string) error) func() {
	testFaultHooksMu.Lock()
	previous := storeTestHooks
	storeTestHooks = testFaultHooks{before: before}
	testFaultHooksMu.Unlock()

	return func() {
		testFaultHooksMu.Lock()
		storeTestHooks = previous
		testFaultHooksMu.Unlock()
	}
}

func applyTestFault(point string) error {
	testFaultHooksMu.RLock()
	hooks := storeTestHooks
	testFaultHooksMu.RUnlock()
	if hooks.before == nil {
		return nil
	}
	return hooks.before(point)
}
