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
}

func (queue *Queue[T]) Enqueue(value T) {
	if _, loaded := queue.barrier.LoadOrStore(value.String(), struct{}{}); loaded {
		return
	}
	queue.append(value)
	queue.tryUnshift()
}

func (queue *Queue[T]) Dequeue() (value T) {
	defer func() {
		queue.tryUnshift()
		queue.barrier.Delete(value.String())
	}()
	return <-queue.pipe
}

func (queue *Queue[T]) C() (chan T, func()) {
	result := make(chan T)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case result <- queue.Dequeue():
			}
		}
	}()

	stop := once(func() {
		cancel()
		<-done
	})

	return result, stop
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
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case value := <-c:
				queue.Enqueue(value)
			}
		}
	}()

	stop := once(func() {
		cancel()
		<-done
	})

	return &queue, stop
}

func once(fn func()) func() {
	var once sync.Once
	return func() { once.Do(fn) }
}
