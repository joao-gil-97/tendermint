package lite

import (
	"fmt"
	"sync"
)

// ConcurrentProvider is a provider which is safe to use by multiple threads.
type ConcurrentProvider struct {
	UpdatingProvider

	// pending map to synchronize concurrent verification requests
	mtx                  sync.Mutex
	pendingVerifications map[pendingKey]*pendingResult
}

// convenience to create the key for the lookup map
type pendingKey struct {
	chainID string
	height  int64
}

// used to cache the result from underlying UpdatingProvider.
type pendingResult struct {
	wait chan struct{}
	err  error // cached result.
}

// NewConcurrentProvider creates a ConcurrentProvider using the given
// UpdatingProvider.
func NewConcurrentProvider(up UpdatingProvider) *ConcurrentProvider {
	return &ConcurrentProvider{
		UpdatingProvider:     up,
		pendingVerifications: make(map[pendingKey]*pendingResult),
	}
}

// Returns the unique pending request for all identical calls to
// joinConcurrency(chainID,height), and returns true for isFirstCall only for
// the first call, which should call the returned callback w/ results if any.
//
// NOTE: The callback must be called, otherwise there will be memory leaks.
//
// Other subsequent calls should just return pr.err.
// This is a separate function, primarily to make mtx unlocking more
// obviously safe via defer.
func (cp *ConcurrentProvider) joinConcurrency(chainID string, height int64) (pr *pendingResult, isFirstCall bool, callback func(error)) {
	cp.mtx.Lock()
	defer cp.mtx.Unlock()

	pk := pendingKey{chainID, height}

	if pr = cp.pendingVerifications[pk]; pr != nil {
		<-pr.wait
		return pr, false, nil
	}

	pr = &pendingResult{wait: make(chan struct{}), err: nil}
	cp.pendingVerifications[pk] = pr

	// The caller must call this, otherwise there will be memory leaks.
	return pr, true, func(err error) {
		// NOTE: other result parameters can be added here.
		pr.err = err

		// *After* setting the results, *then* call close(pr.wait).
		close(pr.wait)

		cp.mtx.Lock()
		delete(cp.pendingVerifications, pk)
		cp.mtx.Unlock()
	}
}

// UpdateToHeight implements UpdatingProvider.
func (cp *ConcurrentProvider) UpdateToHeight(chainID string, height int64) error {
	// Performs synchronization for multi-threads verifications at the same height.
	pr, isFirstCall, callback := cp.joinConcurrency(chainID, height)

	if isFirstCall {
		var err error
		// Use a defer in case UpdateToHeight itself fails.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("Recovered from panic: %v", r)
			}
			callback(err)
		}()
		err = cp.UpdatingProvider.UpdateToHeight(chainID, height)
		return err
	}

	// Is not the first call, so return the error from previous concurrent calls.
	if callback != nil {
		panic("expected callback to be nil")
	}
	return pr.err
}