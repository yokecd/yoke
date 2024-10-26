package main

import (
	"fmt"
	"sync"
)

type Queue[T fmt.Stringer] struct {
	barrier *sync.Map
	buffer  []T
	lock    *sync.Mutex
	pipe    chan T
}

func (queue *Queue[T]) Push(value T) {
	if _, loaded := queue.barrier.LoadOrStore(value.String(), struct{}{}); loaded {
		return
	}

	queue.lock.Lock()
	defer queue.lock.Unlock()

	queue.buffer = append(queue.buffer, value)
	for {
		if len(queue.buffer) == 0 {
			break
		}

		next := queue.buffer[0]
		select {
		case queue.pipe <- next:
			queue.buffer = queue.buffer[1:]
		default:
			break
		}
	}
}

func (queue *Queue[T]) Pop() (value T) {
	defer func() {
		queue.barrier.Delete(value.String())
	}()

	return <-queue.pipe
}

func (queue *Queue[T]) C() chan T {
	result := make(chan T)

	go func() {
		for {
			result <- queue.Pop()
		}
	}()

	return result
}

// QueueFromChannel returns a queue that will dedup events based on its string representation as
// determined by fmt.Stringer. The queue needs to know ahead of time the amount of concurrent readers
// that will be pulling from it. This ensures that workers can never deadlock as it will always provide
// enough space for the workers to pull from before writing events to its internal buffer.
func QueueFromChannel[T fmt.Stringer](c chan T, concurrency int) *Queue[T] {
	queue := Queue[T]{
		barrier: &sync.Map{},
		buffer:  []T{},
		lock:    &sync.Mutex{},
		pipe:    make(chan T, max(concurrency, 1)),
	}

	go func() {
		for value := range c {
			queue.Push(value)
		}
	}()

	return &queue
}
