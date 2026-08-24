package resourcelock

import "sync"

// Pool keeps one mutex per resource while it is held or has waiters. Idle
// entries are removed automatically so deleted resource IDs do not accumulate.
type Pool struct {
	mu    sync.Mutex
	locks map[string]*Lock
}

type Lock struct {
	mutex sync.Mutex
	pool  *Pool
	key   string
	refs  int
}

func (pool *Pool) Get(key string) *Lock {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.locks == nil {
		pool.locks = make(map[string]*Lock)
	}
	lock := pool.locks[key]
	if lock == nil {
		lock = &Lock{pool: pool, key: key}
		pool.locks[key] = lock
	}
	lock.refs++
	return lock
}

func (lock *Lock) Lock() {
	lock.mutex.Lock()
}

func (lock *Lock) TryLock() bool {
	if lock.mutex.TryLock() {
		return true
	}
	lock.releaseReference()
	return false
}

func (lock *Lock) Unlock() {
	lock.mutex.Unlock()
	lock.releaseReference()
}

func (lock *Lock) releaseReference() {
	lock.pool.mu.Lock()
	lock.refs--
	if lock.refs == 0 && lock.pool.locks[lock.key] == lock {
		delete(lock.pool.locks, lock.key)
	}
	lock.pool.mu.Unlock()
}
