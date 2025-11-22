package ctrl

import (
	"context"
	"fmt"
	"sync"

	"github.com/yokecd/yoke/internal/xsync"
)

type Queue[T fmt.Stringer] struct {
	barrier *xsync.Map[string, struct{}]
	buffer  []T
	lock    *sync.Mutex
	pipe    chan T
	C       chan T
	Stop    func()
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
			queue.buffer = queue.buffer[1:]
		default:
			return
		}
	}
}

// NewQueue returns a queue that will dedup events based on its string representation as
// determined by fmt.Stringer.
func NewQueue[T fmt.Stringer]() *Queue[T] {
	queue := Queue[T]{
		barrier: &xsync.Map[string, struct{}]{},
		buffer:  []T{},
		lock:    &sync.Mutex{},
		pipe:    make(chan T, 1),
		C:       make(chan T),
	}

	done := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case value := <-queue.pipe:
				select {
				case <-ctx.Done():
					return
				case queue.C <- value:
					queue.barrier.Delete(value.String())
					queue.tryUnshift()
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
