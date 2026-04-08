package ctrl

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/davidmdm/x/xsync"
)

type Queue[T fmt.Stringer] struct {
	barrier   *xsync.Map[string, struct{}]
	buffer    []T
	lock      *sync.Mutex
	semaphore chan struct{}
	pipe      chan T
	C         chan T
	Stop      func()
}

func (queue *Queue[T]) Enqueue(value T) {
	if _, loaded := queue.barrier.LoadOrStore(value.String(), struct{}{}); loaded {
		return
	}
	queue.append(value)
	queue.tryUnshift()
}

func (queue *Queue[T]) append(value T) {
	queue.lock.Lock()
	defer queue.lock.Unlock()

	queue.buffer = append(queue.buffer, value)
}

func (queue *Queue[t]) tryUnshift() {
	queue.lock.Lock()
	defer queue.lock.Unlock()

	for {
		if len(queue.buffer) == 0 {
			return
		}

		next := queue.buffer[0]
		select {
		case queue.pipe <- next:
			queue.buffer = slices.Delete(queue.buffer, 0, 1)
		default:
			return
		}
	}
}

// NewQueue returns a queue that will dedup events based on its string representation as
// determined by fmt.Stringer.
func NewQueue[T fmt.Stringer](concurrency int) *Queue[T] {
	queue := Queue[T]{
		barrier:   &xsync.Map[string, struct{}]{},
		buffer:    []T{},
		lock:      &sync.Mutex{},
		pipe:      make(chan T, 1),
		C:         make(chan T),
		semaphore: make(chan struct{}, concurrency),
	}

	done := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-queue.semaphore:
				select {
				case <-ctx.Done():
					return
				case value := <-queue.pipe:
					queue.barrier.Delete(value.String())
					select {
					case <-ctx.Done():
					case queue.C <- value:
						queue.tryUnshift()
					}
				}
			}
		}
	}()

	queue.Stop = func() {
		cancel()
		<-done
	}

	return &queue
}

func (queue *Queue[T]) Pull() <-chan T {
	queue.semaphore <- struct{}{}
	return queue.C
}
