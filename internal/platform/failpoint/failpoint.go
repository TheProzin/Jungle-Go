package failpoint

import "sync"

type Hook func() error

var (
	mu    sync.RWMutex
	hooks = map[string]Hook{}
)

func Enable(name string, hook Hook) {
	mu.Lock()
	defer mu.Unlock()
	hooks[name] = hook
}

func Disable(name string) {
	mu.Lock()
	defer mu.Unlock()
	delete(hooks, name)
}

func Reset() {
	mu.Lock()
	defer mu.Unlock()
	hooks = map[string]Hook{}
}

func Trigger(name string) error {
	mu.RLock()
	hook, ok := hooks[name]
	mu.RUnlock()
	if !ok {
		return nil
	}
	return hook()
}
