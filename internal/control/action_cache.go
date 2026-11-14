package control

import (
	"context"
	"sync"
)

// actionCache memoizes read-only Android facts for the duration of one
// action. A switch otherwise asked the device for the same package details,
// zygote processes and nsenter support several times.
type actionCache struct {
	mu     sync.Mutex
	values map[string]cachedOutput
}

type cachedOutput struct {
	out string
	err error
}

func (r *NativeRunner) beginActionCache() {
	r.cache = &actionCache{values: make(map[string]cachedOutput)}
}

func (r *NativeRunner) endActionCache() { r.cache = nil }

// remember runs read once per action and key. Outside an action (status
// polling), every call reaches the device.
func (r *NativeRunner) remember(key string, read func() (string, error)) (string, error) {
	cache := r.cache
	if cache == nil {
		return read()
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if value, ok := cache.values[key]; ok {
		return value.out, value.err
	}
	out, err := read()
	cache.values[key] = cachedOutput{out: out, err: err}
	return out, err
}

func (r *NativeRunner) packageDetails(ctx context.Context, serial string) (string, error) {
	return r.remember(serial+"|dumpsys", func() (string, error) {
		return r.adb(ctx, serial, "shell", "dumpsys", "package", r.cfg.Android.Package)
	})
}
