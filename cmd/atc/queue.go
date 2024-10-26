package main

import (
	"fmt"
	"sync"
)

type buffer[T any] struct {
	mu   sync.Mutex
	data []T
}

func (buffer *buffer[T]) push(value T) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.data = append(buffer.data, value)
}

func (buffer *buffer[T]) pop() (value T, ok bool) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	if len(buffer.data) == 0 {
		return
	}

	value, ok = buffer.data[0], true
	buffer.data = buffer.data[1:]
	return
}

type Queue[T fmt.Stringer] struct {
	barrier *sync.Map
	buffer  *buffer[T]
	pipe    chan T
}

func (queue *Queue[T]) Push(value T) {
	if _, loaded := queue.barrier.LoadOrStore(value.String(), struct{}{}); loaded {
		return
	}

	select {
	case queue.pipe <- value:
	default:
		queue.buffer.push(value)
	}
}

func (queue *Queue[T]) Pop() (value T) {
	defer func() {
		queue.barrier.Delete(value.String())
	}()

	select {
	case value := <-queue.pipe:
		return value
	default:
		value, ok := queue.buffer.pop()
		if ok {
			return value
		}
		return <-queue.pipe
	}
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
		buffer: &buffer[T]{
			mu:   sync.Mutex{},
			data: []T{},
		},
		pipe: make(chan T, max(concurrency, 1)),
	}

	go func() {
		for value := range c {
			queue.Push(value)
		}
	}()

	return &queue
}
