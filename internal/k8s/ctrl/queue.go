package ctrl

import (
	"context"
	"fmt"
	"sync"
)

type Queue[T fmt.Stringer] struct {
	barrier *sync.Map
	buffer  []T
	lock    *sync.Mutex
	pipe    chan T
	C       chan T
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

// QueueFromChannel returns a queue that will dedup events based on its string representation as
// determined by fmt.Stringer.
func QueueFromChannel[T fmt.Stringer](c chan T) (*Queue[T], func()) {
	queue := Queue[T]{
		barrier: &sync.Map{},
		buffer:  []T{},
		lock:    &sync.Mutex{},
		pipe:    make(chan T, 1),
		C:       make(chan T),
	}

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			case value, ok := <-c:
				if !ok {
					return
				}
				queue.Enqueue(value)
			}
		}
	}()

	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			case value := <-queue.pipe:
				select {
				case <-ctx.Done():
					return
				case queue.C <- value:
					queue.tryUnshift()
					queue.barrier.Delete(value.String())
				}
			}
		}
	}()

	stop := func() {
		cancel()
		wg.Wait()
	}

	return &queue, stop
}
